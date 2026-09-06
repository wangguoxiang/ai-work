package services

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ========== 列索引 ==========
const (
	colTID       = 2
	colTimestamp = 18
)

// ========== 数据结构 ==========

// CSVSegment 绑定时间段
type CSVSegment struct {
	BindTS   int64
	UnbindTS int64
}

// CSVTaskStatus 任务状态
type CSVTaskStatus string

const (
	CSVStatusPending CSVTaskStatus = "pending"
	CSVStatusRunning CSVTaskStatus = "running"
	CSVStatusDone    CSVTaskStatus = "done"
	CSVStatusFailed  CSVTaskStatus = "failed"
	CSVStatusResumed CSVTaskStatus = "resumed"
)

// CSVFilterTask 单个过滤任务的状态（含过滤+导入两个阶段）
type CSVFilterTask struct {
	ID         string        `json:"id"`
	TarPath    string        `json:"tar_path"`
	CSVPath    string        `json:"csv_path"`
	OutputPath string        `json:"output_path"`
	Status     CSVTaskStatus `json:"status"`
	Error      string        `json:"error,omitempty"`
	StartedAt  int64         `json:"started_at"`
	UpdatedAt  int64         `json:"updated_at"`
	FinishedAt int64         `json:"finished_at,omitempty"`

	// 过滤列配置(0 表示使用默认值: device_id=2, timestamp=18)
	DeviceIDCol  int `json:"device_id_col,omitempty"`
	TimestampCol int `json:"timestamp_col,omitempty"`

	// SplitByTID 为 true 时按 TID 拆分输出: 每个 TID 生成独立的 *_filtered_<TID>.sql 文件
	SplitByTID bool `json:"split_by_tid,omitempty"`
	// TIDOrder 期望输出的 TID 顺序(仅 split 模式用于稳定文件命名与避免空文件)
	TIDOrder []string `json:"tid_order,omitempty"`

	// OutputFiles 本次任务实际产生的输出文件(单文件=[OutputPath]; split 模式=各 TID 文件)
	OutputFiles []string `json:"output_files,omitempty"`

	LinesDone int64 `json:"lines_done"`
	RawLines  int64 `json:"raw_lines"`
	KeptLines int64 `json:"kept_lines"`
	FirstTS   int64 `json:"first_ts"`
	LastTS    int64 `json:"last_ts"`
	Resumed   bool  `json:"resumed"`
	Pct       int   `json:"pct"`

	// 导入阶段
	ImportStatus   CSVImportStatus `json:"import_status"`
	ImportProgress int             `json:"import_progress"`
	ImportTotal    int64           `json:"import_total"`
	ImportDone     int64           `json:"import_done"`
	ImportError    string          `json:"import_error,omitempty"`

	SubmitOrder int64 `json:"submit_order"`

	cancel chan struct{}
	mu     sync.Mutex
}

func (t *CSVFilterTask) Snapshot() CSVFilterTask {
	t.mu.Lock()
	defer t.mu.Unlock()
	return CSVFilterTask{
		ID: t.ID, TarPath: t.TarPath, CSVPath: t.CSVPath, OutputPath: t.OutputPath,
		Status: t.Status, Error: t.Error,
		StartedAt: t.StartedAt, UpdatedAt: t.UpdatedAt, FinishedAt: t.FinishedAt,
		DeviceIDCol: t.DeviceIDCol, TimestampCol: t.TimestampCol,
		SplitByTID: t.SplitByTID, TIDOrder: t.TIDOrder,
		OutputFiles: t.OutputFiles,
		LinesDone:   t.LinesDone, RawLines: t.RawLines, KeptLines: t.KeptLines,
		FirstTS: t.FirstTS, LastTS: t.LastTS, Resumed: t.Resumed,
		Pct: t.Pct, SubmitOrder: t.SubmitOrder,
		ImportStatus: t.ImportStatus, ImportProgress: t.ImportProgress,
		ImportTotal: t.ImportTotal, ImportDone: t.ImportDone, ImportError: t.ImportError,
	}
}

// deviceIDColIdx 返回实际使用的 device id 列索引(默认 colTID=2)
func (t *CSVFilterTask) deviceIDColIdx() int {
	if t.DeviceIDCol > 0 {
		return t.DeviceIDCol
	}
	return colTID
}

// timestampColIdx 返回实际使用的时间戳列索引(默认 colTimestamp=18)
func (t *CSVFilterTask) timestampColIdx() int {
	if t.TimestampCol > 0 {
		return t.TimestampCol
	}
	return colTimestamp
}

func (t *CSVFilterTask) setStatus(s CSVTaskStatus) {
	t.mu.Lock()
	t.Status = s
	t.UpdatedAt = time.Now().Unix()
	t.mu.Unlock()
}

func (t *CSVFilterTask) setError(err string) {
	t.mu.Lock()
	t.Status = CSVStatusFailed
	t.Error = err
	t.FinishedAt = time.Now().Unix()
	t.mu.Unlock()
}

// setImportStatus 设置导入阶段状态
func (t *CSVFilterTask) setImportStatus(s CSVImportStatus, pct int, done int64) {
	t.mu.Lock()
	t.ImportStatus = s
	t.ImportProgress = pct
	t.ImportDone = done
	t.UpdatedAt = time.Now().Unix()
	t.mu.Unlock()
}

// setImportProgress 更新导入进度
func (t *CSVFilterTask) setImportProgress(pct int, total, done int64) {
	t.mu.Lock()
	t.ImportProgress = pct
	t.ImportTotal = total
	t.ImportDone = done
	t.UpdatedAt = time.Now().Unix()
	t.mu.Unlock()
}

// setImportError 设置导入失败
func (t *CSVFilterTask) setImportError(err string) {
	t.mu.Lock()
	t.ImportStatus = CSVImportFailed
	t.ImportError = err
	t.UpdatedAt = time.Now().Unix()
	t.mu.Unlock()
}

// setImportDone 设置导入完成（保留当前的 ImportTotal 作为 ImportDone）
func (t *CSVFilterTask) setImportDone(total int64) {
	t.mu.Lock()
	t.ImportStatus = CSVImportDone
	t.ImportProgress = 100
	t.ImportDone = total
	t.ImportTotal = total
	t.UpdatedAt = time.Now().Unix()
	t.mu.Unlock()
}

// ========== 进度持久化 ==========

// CSVProgressFile 进度持久化文件
type CSVProgressFile struct {
	TarPath    string `json:"tar_path"`
	TarSize    int64  `json:"tar_size"`
	TarMTime   int64  `json:"tar_mtime"`
	CSVPath    string `json:"csv_path"`
	CSVHash    string `json:"csv_hash"`
	OutputPath string `json:"output_path"`

	// 过滤列配置(用于断点续传时保持一致的列位置)
	DeviceIDCol  int `json:"device_id_col,omitempty"`
	TimestampCol int `json:"timestamp_col,omitempty"`

	LinesDone int64 `json:"lines_done"`
	RawLines  int64 `json:"raw_lines"`
	KeptLines int64 `json:"kept_lines"`
	FirstTS   int64 `json:"first_ts"`
	LastTS    int64 `json:"last_ts"`
	UpdatedAt int64 `json:"updated_at"`
}

func csvProgressPath(outputPath string) string {
	return outputPath + ".progress.json"
}

// LoadCSVProgressAt 加载指定 outputPath 的进度文件(精确匹配)
func LoadCSVProgressAt(tarPath, csvPath, outputPath string) (*CSVProgressFile, bool) {
	data, err := os.ReadFile(csvProgressPath(outputPath))
	if err != nil {
		return nil, false
	}
	var p CSVProgressFile
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, false
	}
	if p.TarPath != tarPath || p.CSVPath != csvPath {
		return nil, false
	}
	st, err := os.Stat(tarPath)
	if err != nil {
		return nil, false
	}
	if p.TarSize != st.Size() || p.TarMTime != st.ModTime().Unix() {
		return nil, false
	}
	return &p, true
}

// LoadCSVProgress 自动查找与 tarPath+csvPath 匹配的进度文件(用于 outputPath 未知的续传场景)
// 同时兼容旧格式(无时间戳)与新格式(带 HHMMSS 时间戳)的进度文件
func LoadCSVProgress(tarPath, csvPath string) (*CSVProgressFile, bool) {
	// 1. 先尝试旧格式(无时间戳)
	if p, ok := LoadCSVProgressAt(tarPath, csvPath, csvLegacyOutputPath(tarPath)); ok {
		return p, true
	}
	// 2. 搜索带时间戳的进度文件,按修改时间倒序取最新的
	dir := filepath.Dir(tarPath)
	base := strings.TrimSuffix(strings.TrimSuffix(filepath.Base(tarPath), ".gz"), ".tar")
	pattern := filepath.Join(dir, base+"_filtered_*.sql.progress.json")
	matches, _ := filepath.Glob(pattern)
	if len(matches) == 0 {
		return nil, false
	}
	sort.Slice(matches, func(i, j int) bool {
		si, errI := os.Stat(matches[i])
		sj, errJ := os.Stat(matches[j])
		if errI != nil || errJ != nil {
			return false
		}
		return si.ModTime().After(sj.ModTime())
	})
	for _, m := range matches {
		data, err := os.ReadFile(m)
		if err != nil {
			continue
		}
		var p CSVProgressFile
		if err := json.Unmarshal(data, &p); err != nil {
			continue
		}
		if p.TarPath != tarPath || p.CSVPath != csvPath {
			continue
		}
		st, err := os.Stat(tarPath)
		if err != nil {
			continue
		}
		if p.TarSize != st.Size() || p.TarMTime != st.ModTime().Unix() {
			continue
		}
		if p.OutputPath == "" {
			continue
		}
		return &p, true
	}
	return nil, false
}

func saveCSVProgress(p *CSVProgressFile) error {
	p.UpdatedAt = time.Now().Unix()
	data, _ := json.MarshalIndent(p, "", "  ")
	tmp := csvProgressPath(p.OutputPath) + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, csvProgressPath(p.OutputPath))
}

func clearCSVProgress(outputPath string) {
	_ = os.Remove(csvProgressPath(outputPath))
}

// clearSplitOutputs 删除某 OutputPath 派生出的历史按TID分桶文件(<stem>_<TID>.sql)
// 仅匹配形如 "<stem>_<任意>.sql" 的文件, 前缀精确, 不会误删无关文件。
func clearSplitOutputs(outputPath string) {
	ext := filepath.Ext(outputPath)
	stem := strings.TrimSuffix(outputPath, ext)
	pattern := stem + "_*" + ext
	matches, _ := filepath.Glob(pattern)
	for _, m := range matches {
		_ = os.Remove(m)
	}
	_ = os.Remove(csvProgressPath(outputPath))
}

// ========== CSVFilterTaskManager ==========

// CSVFilterTaskManager 管理 CSV 过滤任务
type CSVFilterTaskManager struct {
	mu      sync.RWMutex
	tasks   map[string]*CSVFilterTask
	Workers int
	order   int64
}

// NewCSVFilterTaskManager 创建管理器
func NewCSVFilterTaskManager() *CSVFilterTaskManager {
	return &CSVFilterTaskManager{tasks: make(map[string]*CSVFilterTask)}
}

func csvTaskID(tarPath, csvPath string, deviceIDCol int) string {
	if deviceIDCol <= 0 {
		deviceIDCol = colTID
	}
	return fmt.Sprintf("%x", simpleHash(tarPath+"|"+csvPath+"|"+strconv.Itoa(deviceIDCol)))
}

// csvTaskIDSplit 与 csvTaskID 一致, 但将 split 标志纳入哈希, 避免与同 tar+csv 的单文件任务冲突
func csvTaskIDSplit(tarPath, csvPath string, deviceIDCol int, split bool) string {
	if !split {
		return csvTaskID(tarPath, csvPath, deviceIDCol)
	}
	if deviceIDCol <= 0 {
		deviceIDCol = colTID
	}
	return fmt.Sprintf("%x", simpleHash(tarPath+"|"+csvPath+"|"+strconv.Itoa(deviceIDCol)+"|split"))
}

func simpleHash(s string) uint64 {
	var h uint64 = 1469598103934665603
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= 1099511628211
	}
	return h
}

// Get 获取任务
func (m *CSVFilterTaskManager) Get(id string) (*CSVFilterTask, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	t, ok := m.tasks[id]
	return t, ok
}

// List 列出所有任务(按 submit_order 降序)
func (m *CSVFilterTaskManager) List() []CSVFilterTask {
	m.mu.RLock()
	res := make([]CSVFilterTask, 0, len(m.tasks))
	for _, t := range m.tasks {
		res = append(res, t.Snapshot())
	}
	m.mu.RUnlock()
	sort.Slice(res, func(i, j int) bool {
		return res[i].SubmitOrder > res[j].SubmitOrder
	})
	return res
}

// Cancel 取消任务
func (m *CSVFilterTaskManager) Cancel(id string) bool {
	m.mu.RLock()
	t, ok := m.tasks[id]
	m.mu.RUnlock()
	if !ok {
		return false
	}
	t.mu.Lock()
	running := t.Status == CSVStatusRunning || t.Status == CSVStatusPending
	t.mu.Unlock()
	if running {
		close(t.cancel)
	}
	return true
}

// SetStatus 设置任务状态
func (t *CSVFilterTask) SetStatus(s CSVTaskStatus) {
	t.setStatus(s)
}

// SetError 设置任务错误
func (t *CSVFilterTask) SetError(err string) {
	t.setError(err)
}

// SubmitOpts 过滤列配置选项
type SubmitOpts struct {
	DeviceIDCol  int // device id 在 INSERT VALUES 中的列索引(0-based), 0=默认2
	TimestampCol int // 时间戳列索引, 0=默认18
	// SplitByTID 为 true 时按 TID 拆分输出(每个TID一个独立SQL文件)
	SplitByTID bool
	// TIDOrder 期望输出的 TID 顺序(仅 split 模式)
	TIDOrder []string
}

// Submit 提交新任务
func (m *CSVFilterTaskManager) Submit(tarPath, csvPath, outputPath string, restart bool, groupCancel chan struct{}, opts ...SubmitOpts) (*CSVFilterTask, error) {
	deviceIDCol := colTID
	timestampCol := colTimestamp
	splitByTID := false
	var tidOrder []string
	if len(opts) > 0 {
		if opts[0].DeviceIDCol > 0 {
			deviceIDCol = opts[0].DeviceIDCol
		}
		if opts[0].TimestampCol > 0 {
			timestampCol = opts[0].TimestampCol
		}
		splitByTID = opts[0].SplitByTID
		tidOrder = opts[0].TIDOrder
	}
	id := csvTaskIDSplit(tarPath, csvPath, deviceIDCol, splitByTID)

	m.mu.Lock()
	if existing, ok := m.tasks[id]; ok && (existing.Status == CSVStatusRunning || existing.Status == CSVStatusPending) {
		m.mu.Unlock()
		return existing, nil
	}
	m.mu.Unlock()

	// 确定输出路径:
	// - 用户显式指定:直接使用
	// - 未指定且非 restart:尝试从已有进度文件恢复(续传)
	// - 其他情况:生成带 HHMMSS 时间戳的新路径,避免覆盖旧文件
	if outputPath == "" {
		if !restart {
			if p, ok := LoadCSVProgress(tarPath, csvPath); ok && p.OutputPath != "" {
				outputPath = p.OutputPath
			}
		}
		if outputPath == "" {
			outputPath = csvDefaultOutputPath(tarPath)
		}
	}
	if restart {
		clearCSVProgress(outputPath)
		_ = os.Remove(outputPath)
	}
	cancelCh := groupCancel
	if cancelCh == nil {
		cancelCh = make(chan struct{})
	}
	m.mu.Lock()
	m.order++
	t := &CSVFilterTask{
		ID: id, TarPath: tarPath, CSVPath: csvPath, OutputPath: outputPath,
		Status: CSVStatusPending, StartedAt: time.Now().Unix(), UpdatedAt: time.Now().Unix(),
		DeviceIDCol: deviceIDCol, TimestampCol: timestampCol,
		SplitByTID: splitByTID, TIDOrder: tidOrder,
		cancel:      cancelCh,
		SubmitOrder: m.order,
	}
	m.tasks[id] = t
	m.mu.Unlock()
	return t, nil
}

func csvDefaultOutputPath(tarPath string) string {
	ext := filepath.Ext(tarPath)
	base := tarPath[:len(tarPath)-len(ext)]
	if strings.HasSuffix(base, ".tar") {
		base = base[:len(base)-4]
	}
	// 追加 HHMMSS 时间戳,避免同一 tar.gz 多次过滤时覆盖旧文件
	ts := time.Now().Format("150405")
	candidate := fmt.Sprintf("%s_filtered_%s.sql", base, ts)
	// 同一秒内重复提交时追加序号,保证唯一
	for i := 1; i < 1000; i++ {
		if _, err := os.Stat(candidate); os.IsNotExist(err) {
			return candidate
		}
		candidate = fmt.Sprintf("%s_filtered_%s_%d.sql", base, ts, i)
	}
	return candidate
}

// csvLegacyOutputPath 返回旧格式(无时间戳)的输出路径,仅用于向后兼容进度文件查找
func csvLegacyOutputPath(tarPath string) string {
	ext := filepath.Ext(tarPath)
	base := tarPath[:len(tarPath)-len(ext)]
	if strings.HasSuffix(base, ".tar") {
		base = base[:len(base)-4]
	}
	return base + "_filtered.sql"
}

// CSVDefaultOutputPath 返回默认过滤输出路径(与 Submit 传空 outputPath 时行为一致)
func CSVDefaultOutputPath(tarPath string) string {
	return csvDefaultOutputPath(tarPath)
}

// ========== CSV 解析 ==========

// ReadCSV 读取 CSV,返回 map[tid][]CSVSegment
// 支持两种格式:
//
//	格式A(带表头): tid, vin, plate_no, bind_ts, unbind_ts  — 自动按列名查找
//	格式B(无表头): tid, ..., bind_ts, unbind_ts            — 固定索引(row[2], row[3])
func ReadCSV(path string) (map[string][]CSVSegment, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("打开 CSV 失败: %w", err)
	}
	defer f.Close()

	r := csv.NewReader(f)
	r.TrimLeadingSpace = true
	r.LazyQuotes = true
	r.FieldsPerRecord = -1
	all, err := r.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("解析 CSV 失败: %w", err)
	}
	if len(all) == 0 {
		return nil, fmt.Errorf("CSV 为空")
	}

	// 检测是否有表头，并确定各列索引
	var tidIdx, bindTsIdx, unbindTsIdx int
	hasHeader := false

	firstRow := all[0]
	headerMap := make(map[string]int)
	for i, col := range firstRow {
		clean := strings.TrimSpace(strings.ToLower(col))
		clean = strings.TrimLeft(clean, "\ufeff\u00a0")
		headerMap[clean] = i
	}

	// 检查第一行是否包含常见的表头关键字
	if idx, ok := headerMap["tid"]; ok {
		hasHeader = true
		tidIdx = idx
	} else if _, ok := headerMap["tid"]; !ok && len(firstRow) > 0 {
		// 第一列可能是 TID（无表头格式）
		hasHeader = false
	}

	if hasHeader {
		tidIdx = headerMap["tid"]
		// 查找 bind_ts/未绑时间列
		if idx, ok := headerMap["bind_ts"]; ok {
			bindTsIdx = idx
		} else if idx, ok := headerMap["bind_time"]; ok {
			bindTsIdx = idx
		} else {
			bindTsIdx = 2 // 回退到固定索引
		}
		if idx, ok := headerMap["unbind_ts"]; ok {
			unbindTsIdx = idx
		} else if idx, ok := headerMap["unbind_time"]; ok {
			unbindTsIdx = idx
		} else {
			unbindTsIdx = 3 // 回退到固定索引
		}
	} else {
		tidIdx = 0
		bindTsIdx = 2
		unbindTsIdx = 3
	}

	dataStart := 0
	if hasHeader {
		dataStart = 1
	} else if strings.HasPrefix(strings.ToLower(strings.TrimSpace(all[0][0])), "tid") {
		dataStart = 1
		hasHeader = true
		tidIdx = 0
		bindTsIdx = 2
		unbindTsIdx = 3
	}

	segments := make(map[string][]CSVSegment)
	for _, row := range all[dataStart:] {
		maxIdx := tidIdx
		if bindTsIdx > maxIdx {
			maxIdx = bindTsIdx
		}
		if unbindTsIdx > maxIdx {
			maxIdx = unbindTsIdx
		}
		if len(row) <= maxIdx {
			continue
		}
		for i := range row {
			row[i] = strings.TrimSpace(row[i])
		}
		tid := row[tidIdx]
		if tid == "" {
			continue
		}

		// 支持整数时间戳和日期时间字符串
		bt, err1 := strconv.ParseInt(row[bindTsIdx], 10, 64)
		if err1 != nil {
			// 尝试解析日期时间格式 "2006-01-02 15:04:05"
			bt, err1 = parseDateTimeToUnix(row[bindTsIdx])
			if err1 != nil {
				continue
			}
		}
		ubt := int64(0)
		if row[unbindTsIdx] != "" {
			ubt, err = strconv.ParseInt(row[unbindTsIdx], 10, 64)
			if err != nil {
				ubt, err = parseDateTimeToUnix(row[unbindTsIdx])
				if err != nil {
					// 解绑时间可选，解析失败时视为未解绑
					ubt = 0
				}
			}
		}
		segments[tid] = append(segments[tid], CSVSegment{BindTS: bt, UnbindTS: ubt})
	}
	if len(segments) == 0 {
		return nil, fmt.Errorf("CSV 中未解析到有效数据")
	}
	return segments, nil
}

// parseDateTimeToUnix 尝试解析日期时间字符串为 Unix 时间戳
// 支持格式: "2006-01-02 15:04:05", "2006-01-02T15:04:05", "2006/01/02 15:04:05"
func parseDateTimeToUnix(s string) (int64, error) {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, "'\"")
	formats := []string{
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05",
		"2006/01/02 15:04:05",
		"2006-01-02",
		"2006/01/02",
	}
	for _, f := range formats {
		if t, err := time.Parse(f, s); err == nil {
			return t.Unix(), nil
		}
	}
	return 0, fmt.Errorf("无法解析日期时间: %s", s)
}

// ReadDeviceIDCSV 读取 device id 列表 CSV,返回 map[deviceID][]CSVSegment
// 支持两种格式:
//
//	格式A(带表头): device_id(或 id/sn), bind_ts(可选), unbind_ts(可选) — 自动按列名查找
//	格式B(无表头): 第一列即 device id
//
// 当 CSV 中不包含 bind_ts/unbind_ts 列时,每个 device id 生成 BindTS=0/UnbindTS=0 的段,
// 匹配该设备的所有记录(不过滤时间)。
func ReadDeviceIDCSV(path string) (map[string][]CSVSegment, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("打开 CSV 失败: %w", err)
	}
	defer f.Close()

	r := csv.NewReader(f)
	r.TrimLeadingSpace = true
	r.LazyQuotes = true
	r.FieldsPerRecord = -1
	all, err := r.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("解析 CSV 失败: %w", err)
	}
	if len(all) == 0 {
		return nil, fmt.Errorf("CSV 为空")
	}

	// 检测表头与各列索引
	var idIdx, bindTsIdx, unbindTsIdx int
	hasHeader := false
	hasTimeCols := false

	firstRow := all[0]
	headerMap := make(map[string]int)
	for i, col := range firstRow {
		clean := strings.TrimSpace(strings.ToLower(col))
		clean = strings.TrimLeft(clean, "\ufeff\u00a0")
		headerMap[clean] = i
	}

	// 设备ID列优先级: device_id > deviceid > id > sn
	if idx, ok := headerMap["device_id"]; ok {
		idIdx, hasHeader = idx, true
	} else if idx, ok := headerMap["deviceid"]; ok {
		idIdx, hasHeader = idx, true
	} else if idx, ok := headerMap["id"]; ok {
		idIdx, hasHeader = idx, true
	} else if idx, ok := headerMap["sn"]; ok {
		idIdx, hasHeader = idx, true
	}

	if !hasHeader {
		// 无表头:第一列即 device id;若首行首列看起来像表头关键字则跳过
		idIdx = 0
		head := strings.ToLower(strings.TrimSpace(all[0][0]))
		if head == "device_id" || head == "deviceid" || head == "id" || head == "sn" {
			hasHeader = true
		}
	}

	if hasHeader {
		if idx, ok := headerMap["bind_ts"]; ok {
			bindTsIdx, hasTimeCols = idx, true
		} else if idx, ok := headerMap["bind_time"]; ok {
			bindTsIdx, hasTimeCols = idx, true
		}
		if idx, ok := headerMap["unbind_ts"]; ok {
			unbindTsIdx = idx
		} else if idx, ok := headerMap["unbind_time"]; ok {
			unbindTsIdx = idx
		}
	}

	dataStart := 0
	if hasHeader {
		dataStart = 1
	}

	segments := make(map[string][]CSVSegment)
	for _, row := range all[dataStart:] {
		if len(row) <= idIdx {
			continue
		}
		devID := strings.TrimSpace(row[idIdx])
		devID = strings.Trim(devID, "'\"")
		if devID == "" {
			continue
		}
		if !hasTimeCols {
			// 只有 device id 列表:匹配该设备所有记录
			segments[devID] = append(segments[devID], CSVSegment{BindTS: 0, UnbindTS: 0})
			continue
		}
		if len(row) <= bindTsIdx {
			continue
		}
		bt, err1 := strconv.ParseInt(strings.TrimSpace(row[bindTsIdx]), 10, 64)
		if err1 != nil {
			bt, err1 = parseDateTimeToUnix(row[bindTsIdx])
			if err1 != nil {
				continue
			}
		}
		ubt := int64(0)
		if unbindTsIdx < len(row) && strings.TrimSpace(row[unbindTsIdx]) != "" {
			ubt, err = strconv.ParseInt(strings.TrimSpace(row[unbindTsIdx]), 10, 64)
			if err != nil {
				ubt, err = parseDateTimeToUnix(row[unbindTsIdx])
				if err != nil {
					ubt = 0
				}
			}
		}
		segments[devID] = append(segments[devID], CSVSegment{BindTS: bt, UnbindTS: ubt})
	}
	if len(segments) == 0 {
		return nil, fmt.Errorf("CSV 中未解析到有效 device id")
	}
	return segments, nil
}

// ========== SQL 解析核心函数 ==========

func segmentOverlaps(ts int64, segs []CSVSegment) bool {
	for _, s := range segs {
		if s.UnbindTS == 0 {
			if ts >= s.BindTS {
				return true
			}
		} else if ts >= s.BindTS && ts < s.UnbindTS {
			return true
		}
	}
	return false
}

// splitTuples 状态机:提取 VALUES 后的每个 (...) tuple
func splitTuples(s string) []string {
	var res []string
	var cur strings.Builder
	depth := 0
	inQ := false
	cur.Grow(64)
	flush := func() {
		if cur.Len() > 0 {
			res = append(res, cur.String())
			cur.Reset()
		}
	}
	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch {
		case inQ:
			cur.WriteByte(ch)
			if ch == '\'' {
				if i+1 < len(s) && s[i+1] == '\'' {
					cur.WriteByte(s[i+1])
					i++
				} else {
					inQ = false
				}
			}
		case ch == '\'':
			inQ = true
			cur.WriteByte(ch)
		case ch == '(':
			if depth == 0 {
				cur.Reset()
			}
			depth++
			cur.WriteByte(ch)
		case ch == ')':
			depth--
			cur.WriteByte(ch)
			if depth == 0 {
				flush()
			}
		case depth == 0:
		default:
			cur.WriteByte(ch)
		}
	}
	flush()
	return res
}

func tupleFields(tuple string) []string {
	s := tuple
	if len(s) >= 2 && s[0] == '(' && s[len(s)-1] == ')' {
		s = s[1 : len(s)-1]
	}
	var res []string
	var cur strings.Builder
	inQ := false
	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch {
		case inQ:
			cur.WriteByte(ch)
			if ch == '\'' {
				if i+1 < len(s) && s[i+1] == '\'' {
					cur.WriteByte(s[i+1])
					i++
				} else {
					inQ = false
				}
			}
		case ch == '\'':
			inQ = true
			cur.WriteByte(ch)
		case ch == ',':
			res = append(res, strings.TrimSpace(cur.String()))
			cur.Reset()
		default:
			cur.WriteByte(ch)
		}
	}
	if cur.Len() > 0 {
		res = append(res, strings.TrimSpace(cur.String()))
	}
	return res
}

func stripSQLQuotes(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && s[0] == '\'' && s[len(s)-1] == '\'' {
		s = s[1 : len(s)-1]
		s = strings.ReplaceAll(s, "''", "'")
	}
	return s
}

func extractValuesPart(line string) (head, valuesPart string, ok bool) {
	upper := strings.ToUpper(line)
	idx := strings.Index(upper, "VALUES")
	if idx < 0 {
		return "", "", false
	}
	head = line[:idx+len("VALUES")]
	valuesPart = strings.TrimRight(line[idx+len("VALUES"):], " \t\r\n;")
	return head, valuesPart, true
}

// insertHead 返回一行 INSERT 的 "INSERT ... VALUES" 前缀(不含 VALUES 之后内容)
func insertHead(line string) string {
	if h, _, ok := extractValuesPart(line); ok {
		return h
	}
	return line
}

// FilterLine 解析单行,返回新行/原始数/保留数/首ts/末ts
// deviceIDCol / tsCol 为 INSERT VALUES 中设备ID列和时间戳列的索引(0-based)
// keptTuple 单条保留的 tuple 及其归属的 device id
type keptTuple struct {
	devID string
	raw   string
	ts    int64
}

// filterLineTuples 解析一行 INSERT,返回按出现顺序保留的 tuples(含归属 devID)与原始条数
// head 为 "INSERT ... VALUES" 前缀; 仅当该行无匹配时 ok=false
func filterLineTuples(line string, segments map[string][]CSVSegment, preSkipped map[string]bool, deviceIDCol, tsCol int) (head string, kept []keptTuple, lineRaw int, firstTS, lastTS int64, ok bool) {
	h, valuesPart, ok2 := extractValuesPart(line)
	if !ok2 {
		return "", nil, 0, 0, 0, false
	}
	tuples := splitTuples(valuesPart)
	for _, t := range tuples {
		fields := tupleFields(t)
		lineRaw++
		if len(fields) <= tsCol {
			continue
		}
		ts, err := strconv.ParseInt(strings.TrimSpace(fields[tsCol]), 10, 64)
		if err != nil {
			continue
		}
		lastTS = ts
		devID := ""
		if deviceIDCol < len(fields) {
			devID = stripSQLQuotes(fields[deviceIDCol])
		}
		if preSkipped != nil && preSkipped[devID] {
			continue
		}
		segs, exists := segments[devID]
		if !exists || !segmentOverlaps(ts, segs) {
			continue
		}
		kept = append(kept, keptTuple{devID: devID, raw: t, ts: ts})
		if firstTS == 0 {
			firstTS = ts
		}
	}
	return h, kept, lineRaw, firstTS, lastTS, true
}

// FilterLine 解析单行,返回新行/原始数/保留数/首ts/末ts
// deviceIDCol / tsCol 为 INSERT VALUES 中设备ID列和时间戳列的索引(0-based)
// 保留所有匹配 TID 的 tuple,按原文件中出现顺序拼接成单行(非 split 模式)
func FilterLine(line string, segments map[string][]CSVSegment, preSkipped map[string]bool, deviceIDCol, tsCol int) (newLine string, lineRaw, lineKept int, firstTS, lastTS int64) {
	head, kept, raw, fTS, lTS, ok := filterLineTuples(line, segments, preSkipped, deviceIDCol, tsCol)
	if !ok {
		return line, 0, 0, 0, 0
	}
	lineRaw = raw
	if len(kept) == 0 {
		return "", lineRaw, 0, fTS, lTS
	}
	parts := make([]string, 0, len(kept))
	for _, k := range kept {
		parts = append(parts, k.raw)
		lineKept++
	}
	return head + " " + strings.Join(parts, ",") + ";", lineRaw, lineKept, fTS, lTS
}

// FilterLineBuckets 解析单行,按 TID(device id) 分桶返回每行保留的 tuple。
// 返回 map[tid][]string(每个 TID 的保留 tuple,按出现顺序),用于 split 模式下按 TID 输出独立 SQL。
// 同时返回原始条数 raw 与首/末时间戳。
func FilterLineBuckets(line string, segments map[string][]CSVSegment, preSkipped map[string]bool, deviceIDCol, tsCol int) (buckets map[string][]string, raw int, firstTS, lastTS int64, ok bool) {
	_, kept, r, fTS, lTS, ok2 := filterLineTuples(line, segments, preSkipped, deviceIDCol, tsCol)
	if !ok2 {
		return nil, 0, 0, 0, false
	}
	if len(kept) == 0 {
		return nil, r, fTS, lTS, true
	}
	buckets = make(map[string][]string)
	for _, k := range kept {
		buckets[k.devID] = append(buckets[k.devID], k.raw)
	}
	return buckets, r, fTS, lTS, true
}

// bucketKeptCount 统计分桶中保留的 tuple 总数
func bucketKeptCount(buckets map[string][]string) int {
	n := 0
	for _, v := range buckets {
		n += len(v)
	}
	return n
}

// ========== gzip 文件打开 ==========

// OpenSqlGzip 打开 gzip 压缩的 SQL 文件,支持 tar.gz 和纯 .sql.gz
func OpenSqlGzip(path string) (sqlReader io.Reader, file *os.File, sqlName string, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, "", err
	}
	gzr, err := gzip.NewReader(f)
	if err != nil {
		f.Close()
		return nil, nil, "", fmt.Errorf("gzip 解析失败(可能不是 gzip 文件): %w", err)
	}
	br := bufio.NewReaderSize(gzr, 512)

	hdr, perr := br.Peek(265)
	if perr != nil && perr != io.EOF {
		return br, f, gzipBaseName(path), nil
	}
	if len(hdr) >= 265 && string(hdr[257:262]) == "ustar" {
		tr := tar.NewReader(br)
		for {
			h, e := tr.Next()
			if e == io.EOF {
				f.Close()
				return nil, nil, "", fmt.Errorf("tar 中未找到 .sql 文件")
			}
			if e != nil {
				f.Close()
				return nil, nil, "", fmt.Errorf("读取 tar 失败: %w", e)
			}
			if strings.HasSuffix(strings.ToLower(h.Name), ".sql") {
				return tr, f, h.Name, nil
			}
		}
	}
	return br, f, gzipBaseName(path), nil
}

func gzipBaseName(path string) string {
	base := filepath.Base(path)
	if strings.HasSuffix(strings.ToLower(base), ".gz") {
		base = base[:len(base)-3]
	}
	if base == "" {
		base = filepath.Base(path)
	}
	return base
}

// ========== 任务执行 ==========

// RunTask 运行单个过滤任务
// ========== 输出 sink(单文件 或 按TID多文件) ==========

// csvOutputSink 封装过滤结果输出:
//   - 非 split: 写入单个 OutputPath 文件(保持原有逻辑)
//   - split:    按 TID 写入多个独立文件, 文件名在 OutputPath 基础上插入干净化的 TID
type csvOutputSink struct {
	split   bool
	base    string // 单文件路径(非split)或分桶路径模板(split)
	dir     string
	ext     string
	singleF *os.File
	singleW *bufio.Writer

	tidF map[string]*os.File // key = 完整文件路径
	tidW map[string]*bufio.Writer
	// 已创建(实际写入过)的文件路径, 用于后续逐个导入
	Created []string
}

// sanitizeTID 清理 TID 用于文件名(去除路径分隔与危险字符)
func sanitizeTID(tid string) string {
	r := strings.NewReplacer("/", "_", "\\", "_", ":", "_", "*", "_", "?", "_", "\"", "_", "<", "_", ">", "_", "|", "_", " ", "_")
	s := r.Replace(strings.TrimSpace(tid))
	if s == "" {
		s = "unknown"
	}
	return s
}

// splitPathForTID 返回 split 模式下某 TID 的输出文件路径
func (s *csvOutputSink) splitPathForTID(tid string) string {
	return filepath.Join(s.dir, s.base+"_"+sanitizeTID(tid)+s.ext)
}

// newCSVOutputSink 创建输出 sink。
// basePath: 单文件路径; split 模式下以其目录/基底派生各 TID 文件。
func newCSVOutputSink(split bool, basePath string) *csvOutputSink {
	if !split {
		return &csvOutputSink{split: false, base: basePath}
	}
	dir := filepath.Dir(basePath)
	ext := filepath.Ext(basePath) // .sql
	stem := strings.TrimSuffix(basePath, ext)
	return &csvOutputSink{
		split: true, base: filepath.Base(stem), dir: dir, ext: ext,
		tidF: make(map[string]*os.File),
		tidW: make(map[string]*bufio.Writer),
	}
}

// open 打开输出(非split: 创建/追加单文件; split: 惰性打开各TID文件, 这里仅预留)
func (s *csvOutputSink) open(appendMode bool) error {
	if s.split {
		return nil
	}
	var err error
	if appendMode {
		s.singleF, err = os.OpenFile(s.base, os.O_WRONLY|os.O_APPEND, 0644)
	} else {
		s.singleF, err = os.Create(s.base)
	}
	if err != nil {
		return err
	}
	s.singleW = bufio.NewWriterSize(s.singleF, 1<<20)
	return nil
}

// writeLine 写入一行过滤结果。
// line: 非split模式下已拼接好的完整 INSERT 行(可能为空=无匹配);
// buckets: split 模式下 map[tid][]tuple(该行各TID保留的tuple);
// head: INSERT 前缀(用于 split 模式重建每行)。
func (s *csvOutputSink) writeLine(line, head string, buckets map[string][]string) error {
	if !s.split {
		if line == "" {
			return nil
		}
		_, err := s.singleW.WriteString(line)
		if err != nil {
			return err
		}
		return s.singleW.WriteByte('\n')
	}
	// split 模式: 每个 TID 独立写一行完整 INSERT
	for tid, tups := range buckets {
		if len(tups) == 0 {
			continue
		}
		p := s.splitPathForTID(tid)
		w, ok := s.tidW[p]
		if !ok {
			f, err := os.Create(p)
			if err != nil {
				return err
			}
			s.tidF[p] = f
			s.tidW[p] = bufio.NewWriterSize(f, 1<<20)
			w = s.tidW[p]
			s.Created = append(s.Created, p)
		}
		if _, err := w.WriteString(head + " " + strings.Join(tups, ",") + ";\n"); err != nil {
			return err
		}
	}
	return nil
}

// flush 冲刷所有 writer
func (s *csvOutputSink) flush() error {
	if !s.split {
		if s.singleW != nil {
			return s.singleW.Flush()
		}
		return nil
	}
	for _, w := range s.tidW {
		if err := w.Flush(); err != nil {
			return err
		}
	}
	return nil
}

// close 关闭所有文件
func (s *csvOutputSink) close() error {
	if !s.split {
		if s.singleF != nil {
			return s.singleF.Close()
		}
		return nil
	}
	var firstErr error
	for _, f := range s.tidF {
		if err := f.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// removeCreated 删除所有已创建的输出文件(失败时清理用)
func (s *csvOutputSink) removeCreated() {
	for _, p := range s.Created {
		_ = os.Remove(p)
	}
}

// ========== 任务执行 ==========

func (m *CSVFilterTaskManager) RunTask(t *CSVFilterTask, segments map[string][]CSVSegment, prog *CSVProgressFile) {
	t.setStatus(CSVStatusRunning)
	startTime := time.Now()
	log.Printf("[CSV过滤] 开始 tar=%s csv=%s output=%s (device_id列=%d, 时间戳列=%d)",
		t.TarPath, t.CSVPath, t.OutputPath, t.deviceIDColIdx(), t.timestampColIdx())

	split := t.SplitByTID

	// split 模式不支持断点续传(多文件进度复杂), 强制从头开始并清理历史分桶输出
	if split {
		prog = nil
		clearSplitOutputs(t.OutputPath)
	}

	// 进度文件列配置不一致时(例如同一 tar+csv 换了过滤列)忽略旧进度,从头开始
	if prog != nil {
		progDevCol := prog.DeviceIDCol
		progTsCol := prog.TimestampCol
		if progDevCol <= 0 {
			progDevCol = colTID
		}
		if progTsCol <= 0 {
			progTsCol = colTimestamp
		}
		if progDevCol != t.deviceIDColIdx() || progTsCol != t.timestampColIdx() {
			log.Printf("[CSV过滤] 进度文件列配置不一致(device_id %d->%d, ts %d->%d), 重新开始",
				progDevCol, t.deviceIDColIdx(), progTsCol, t.timestampColIdx())
			prog = nil
			clearCSVProgress(t.OutputPath)
		}
	}

	var resumeFrom int64
	if prog != nil {
		resumeFrom = prog.LinesDone
		log.Printf("[CSV过滤] 续传: 从第 %d 行继续(已写入 %d 行, 保留 %d 条)", resumeFrom+1, resumeFrom, prog.KeptLines)
	}

	totalSegs := 0
	for _, s := range segments {
		totalSegs += len(s)
	}
	log.Printf("[CSV过滤] %d 个 TID, %d 个时间段%s", len(segments), totalSegs,
		func() string {
			if split {
				return " (按TID拆分输出)"
			}
			return ""
		}())

	tarStat, err := os.Stat(t.TarPath)
	if err != nil {
		t.setError("tar.gz 不存在: " + err.Error())
		return
	}

	tr, tarFile, sqlName, err := OpenSqlGzip(t.TarPath)
	if err != nil {
		t.setError("打开 gzip 失败: " + err.Error())
		return
	}
	defer tarFile.Close()
	_ = sqlName

	linesDone := resumeFrom
	var firstTS, lastTS int64
	var rawLines, keptLines int64

	if prog != nil {
		rawLines = prog.RawLines
		keptLines = prog.KeptLines
		firstTS = prog.FirstTS
		lastTS = prog.LastTS
	}

	var totalLines int64

	br := bufio.NewReaderSize(tr, 1<<20)

	// 输出 sink(单文件 或 按TID多文件)
	sink := newCSVOutputSink(split, t.OutputPath)
	if err := sink.open(prog != nil); err != nil {
		t.setError("打开输出文件失败: " + err.Error())
		return
	}
	defer sink.close()

	progressSaveEvery := int64(500)
	lastSavedAt := int64(0)
	progressSaveFails := 0

	curProg := &CSVProgressFile{
		TarPath: t.TarPath, TarSize: tarStat.Size(), TarMTime: tarStat.ModTime().Unix(),
		CSVPath: t.CSVPath, OutputPath: t.OutputPath,
		DeviceIDCol: t.DeviceIDCol, TimestampCol: t.TimestampCol,
		LinesDone: linesDone, RawLines: rawLines, KeptLines: keptLines,
		FirstTS: firstTS, LastTS: lastTS,
	}

	type lineResult struct {
		line    string // 非split模式拼接好的行(可能为空)
		head    string // split 模式需要的前缀
		buckets map[string][]string
		raw     int
		kept    int
		firstTS int64
		lastTS  int64
	}

	isInsert := func(s string) bool {
		return s != "" && strings.HasPrefix(strings.ToUpper(s), "INSERT")
	}

	var writtenLines int64

	if prog != nil {
		log.Printf("[CSV过滤] 正在快速跳过前 %d 行...", linesDone)
		skipStart := time.Now()
		remaining := linesDone - totalLines
		for remaining > 0 {
			peeked, perr := br.Peek(256 << 10)
			if len(peeked) == 0 {
				if perr != nil {
					break
				}
				continue
			}
			nls := 0
			lastNl := -1
			for i, b := range peeked {
				if b == '\n' {
					nls++
					if int64(nls) == remaining {
						lastNl = i
						break
					}
				}
			}
			if lastNl >= 0 {
				br.Discard(lastNl + 1)
				totalLines += remaining
				remaining = 0
			} else {
				br.Discard(len(peeked))
				totalLines += int64(nls)
				remaining -= int64(nls)
				if perr != nil {
					break
				}
			}
		}
		log.Printf("[CSV过滤] 跳过完成, 耗时 %s", time.Since(skipStart).Round(time.Millisecond))
	} else {
		phase1Done := false
		for !phase1Done {
			lineBytes, rerr := br.ReadBytes('\n')
			if len(lineBytes) == 0 && rerr != nil {
				phase1Done = true
				break
			}
			totalLines++
			trimmed := strings.TrimSpace(string(lineBytes))
			if !isInsert(trimmed) {
				continue
			}
			var res lineResult
			if split {
				buckets, raw, fTS, lTS, okB := FilterLineBuckets(trimmed, segments, nil, t.deviceIDColIdx(), t.timestampColIdx())
				if okB {
					res = lineResult{head: insertHead(trimmed), buckets: buckets, raw: raw, kept: bucketKeptCount(buckets), firstTS: fTS, lastTS: lTS}
				} else {
					res = lineResult{raw: 0}
				}
			} else {
				nl, raw, kept, fTS, lTS := FilterLine(trimmed, segments, nil, t.deviceIDColIdx(), t.timestampColIdx())
				res = lineResult{line: nl, raw: raw, kept: kept, firstTS: fTS, lastTS: lTS}
			}
			if res.firstTS != 0 {
				firstTS = res.firstTS
			}
			rawLines += int64(res.raw)
			keptLines += int64(res.kept)
			if res.lastTS != 0 {
				lastTS = res.lastTS
			}
			writtenLines++
			if err := sink.writeLine(res.line, res.head, res.buckets); err != nil {
				log.Printf("[CSV过滤] 写首行失败: %v", err)
			}
			if err := sink.flush(); err != nil {
				log.Printf("[CSV过滤] flush 首行失败: %v", err)
			}
			phase1Done = true
		}
	}

	numWorkers := m.Workers
	if numWorkers <= 0 {
		numWorkers = runtime.NumCPU()
	}
	const batchSize = 200

	cancelled := false
	fatalErr := ""

	saveProgressAndLog := func() {
		if err := sink.flush(); err != nil {
			log.Printf("[CSV过滤] flush 失败: %v", err)
		}
		if !split {
			curProg.LinesDone = totalLines
			curProg.RawLines = rawLines
			curProg.KeptLines = keptLines
			curProg.FirstTS = firstTS
			curProg.LastTS = lastTS
			if err := saveCSVProgress(curProg); err != nil {
				progressSaveFails++
				log.Printf("[CSV过滤] 保存进度失败(连续%d次): %v", progressSaveFails, err)
				if progressSaveFails >= 3 {
					fatalErr = "进度持久化连续失败3次: " + err.Error()
				}
				return
			}
			progressSaveFails = 0
		}
		lastSavedAt = writtenLines

		pct := 0
		if tarStat.Size() > 0 {
			if pos, e := tarFile.Seek(0, io.SeekCurrent); e == nil {
				pct = int(pos * 100 / tarStat.Size())
				if pct < 0 {
					pct = 0
				}
				if pct > 99 {
					pct = 99
				}
			}
		}
		t.mu.Lock()
		t.LinesDone = totalLines
		t.RawLines = rawLines
		t.KeptLines = keptLines
		if firstTS != 0 {
			t.FirstTS = firstTS
		}
		if lastTS != 0 {
			t.LastTS = lastTS
		}
		t.Pct = pct
		t.UpdatedAt = time.Now().Unix()
		t.mu.Unlock()
		log.Printf("[CSV过滤 进度 %d%%] 已读 %d 行, 原始 %d 条, 保留 %d 条",
			pct, totalLines, rawLines, keptLines)
	}

	batch := make([]string, 0, batchSize)

	flushBatch := func() error {
		if len(batch) == 0 {
			return nil
		}
		results := make([]lineResult, len(batch))
		var wg sync.WaitGroup
		sem := make(chan struct{}, numWorkers)
		for i, line := range batch {
			select {
			case <-t.cancel:
				cancelled = true
			default:
			}
			if cancelled {
				break
			}
			wg.Add(1)
			sem <- struct{}{}
			go func(idx int, l string) {
				defer wg.Done()
				defer func() { <-sem }()
				if split {
					buckets, raw, fTS, lTS, okB := FilterLineBuckets(l, segments, nil, t.deviceIDColIdx(), t.timestampColIdx())
					if okB {
						results[idx] = lineResult{head: insertHead(l), buckets: buckets, raw: raw, kept: bucketKeptCount(buckets), firstTS: fTS, lastTS: lTS}
					} else {
						results[idx] = lineResult{raw: 0}
					}
				} else {
					nl, raw, kept, fTS, lTS := FilterLine(l, segments, nil, t.deviceIDColIdx(), t.timestampColIdx())
					results[idx] = lineResult{line: nl, raw: raw, kept: kept, firstTS: fTS, lastTS: lTS}
				}
			}(i, line)
		}
		wg.Wait()

		if cancelled {
			return nil
		}
		for _, pr := range results {
			if pr.firstTS != 0 {
				firstTS = pr.firstTS
			}
			rawLines += int64(pr.raw)
			keptLines += int64(pr.kept)
			if pr.lastTS != 0 {
				lastTS = pr.lastTS
			}
			if err := sink.writeLine(pr.line, pr.head, pr.buckets); err != nil {
				sink.flush()
				return err
			}
			writtenLines++
		}
		if writtenLines-lastSavedAt >= progressSaveEvery {
			saveProgressAndLog()
		}
		batch = batch[:0]
		return nil
	}

	for {
		select {
		case <-t.cancel:
			cancelled = true
		default:
		}
		if cancelled {
			break
		}
		lineBytes, rerr := br.ReadBytes('\n')
		if len(lineBytes) == 0 && rerr != nil {
			break
		}
		totalLines++
		trimmed := strings.TrimSpace(string(lineBytes))
		if !isInsert(trimmed) {
			continue
		}
		batch = append(batch, trimmed)

		if len(batch) >= batchSize {
			if err := flushBatch(); err != nil {
				sink.flush()
				sink.close()
				sink.removeCreated()
				t.setError("写入输出失败: " + err.Error())
				return
			}
			if cancelled || fatalErr != "" {
				break
			}
		}
	}

	if !cancelled && fatalErr == "" && len(batch) > 0 {
		if err := flushBatch(); err != nil {
			sink.flush()
			sink.close()
			sink.removeCreated()
			t.setError("写入输出失败: " + err.Error())
			return
		}
	}

	if cancelled || fatalErr != "" {
		sink.flush()
		sink.close()
		sink.removeCreated()
		if !split {
			curProg.LinesDone = totalLines
			curProg.RawLines = rawLines
			curProg.KeptLines = keptLines
			curProg.FirstTS = firstTS
			curProg.LastTS = lastTS
			_ = saveCSVProgress(curProg)
		}
		t.mu.Lock()
		errMsg := "已取消"
		if fatalErr != "" {
			errMsg = fatalErr
		}
		t.Status = CSVStatusFailed
		t.Error = errMsg
		t.FinishedAt = time.Now().Unix()
		t.mu.Unlock()
		log.Printf("[CSV过滤] 取消: 进度已保存(%d 行)", totalLines)
		return
	}

	if err := sink.flush(); err != nil {
		sink.close()
		sink.removeCreated()
		t.setError("flush 失败: " + err.Error())
		return
	}
	sink.close()
	if !split {
		clearCSVProgress(t.OutputPath)
	}

	t.mu.Lock()
	t.Status = CSVStatusDone
	t.LinesDone = totalLines
	t.RawLines = rawLines
	t.KeptLines = keptLines
	t.FirstTS = firstTS
	t.LastTS = lastTS
	t.Pct = 100
	t.FinishedAt = time.Now().Unix()
	if split {
		// 记录各 TID 输出文件(供管道逐个导入)
		t.OutputFiles = append([]string(nil), sink.Created...)
	} else {
		t.OutputFiles = []string{t.OutputPath}
	}
	t.mu.Unlock()
	log.Printf("[CSV过滤] 完成: %d 行, 原始 %d 条, 保留 %d 条, 耗时 %s, 输出文件=%d",
		totalLines, rawLines, keptLines, time.Since(startTime).Round(time.Millisecond), len(t.OutputFiles))
}

// ResumeOnStartup 启动时自动恢复未完成任务
func (m *CSVFilterTaskManager) ResumeOnStartup(dir string) {
	_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".progress.json") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		var p CSVProgressFile
		if json.Unmarshal(data, &p) != nil {
			return nil
		}
		if _, err := os.Stat(p.TarPath); err != nil {
			log.Printf("[CSV过滤 恢复] 跳过 %s: tar 文件不存在(%s)", path, p.TarPath)
			return nil
		}
		if _, err := os.Stat(p.CSVPath); err != nil {
			log.Printf("[CSV过滤 恢复] 跳过 %s: csv 文件不存在(%s)", path, p.CSVPath)
			return nil
		}
		st, err := os.Stat(p.TarPath)
		if err != nil || st.Size() != p.TarSize || st.ModTime().Unix() != p.TarMTime {
			log.Printf("[CSV过滤 恢复] 跳过 %s: tar 文件已变化", path)
			return nil
		}

		id := csvTaskID(p.TarPath, p.CSVPath, p.DeviceIDCol)
		m.mu.Lock()
		if _, exists := m.tasks[id]; exists {
			m.mu.Unlock()
			return nil
		}
		t := &CSVFilterTask{
			ID: id, TarPath: p.TarPath, CSVPath: p.CSVPath, OutputPath: p.OutputPath,
			Status: CSVStatusPending, StartedAt: time.Now().Unix(), UpdatedAt: time.Now().Unix(),
			DeviceIDCol: p.DeviceIDCol, TimestampCol: p.TimestampCol,
			cancel:    make(chan struct{}),
			LinesDone: p.LinesDone, RawLines: p.RawLines, KeptLines: p.KeptLines,
			FirstTS: p.FirstTS, LastTS: p.LastTS, Resumed: p.LinesDone > 0,
		}
		m.tasks[id] = t
		m.mu.Unlock()
		log.Printf("[CSV过滤 恢复] 恢复任务 %s: %s (已写入 %d 行)", id, p.TarPath, p.LinesDone)

		go func(pp CSVProgressFile, tt *CSVFilterTask) {
			// 按进度文件记录的列配置选择对应的 CSV 解析器
			var segs map[string][]CSVSegment
			var err error
			if pp.DeviceIDCol > 0 {
				segs, err = ReadDeviceIDCSV(pp.CSVPath)
			} else {
				segs, err = ReadCSV(pp.CSVPath)
			}
			if err != nil {
				tt.setError("CSV 解析失败: " + err.Error())
				return
			}
			m.RunTask(tt, segs, &pp)
		}(p, t)
		return nil
	})
}
