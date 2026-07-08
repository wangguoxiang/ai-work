package services

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"gps-archive-tool/internal/config"
)

// ========== 常量 ==========

const tiandituReverseURL = "https://geo.util.linketech.cn/api/tianditu/reverse"

// ========== 任务状态 ==========

// ReverseGeoStatus 逆地址转换任务状态
type ReverseGeoStatus string

const (
	ReverseGeoPending   ReverseGeoStatus = "pending"
	ReverseGeoRunning   ReverseGeoStatus = "running"
	ReverseGeoCompleted ReverseGeoStatus = "completed"
	ReverseGeoFailed    ReverseGeoStatus = "failed"
)

// ReverseGeoTask 逆地址转换任务
type ReverseGeoTask struct {
	ID          string             `json:"id"`
	FileName    string             `json:"file_name"`
	FilePath    string             `json:"file_path"`
	OutputDir   string             `json:"output_dir"`
	OutputFiles []string           `json:"output_files,omitempty"`
	Status      ReverseGeoStatus   `json:"status"`
	Error       string             `json:"error,omitempty"`
	TotalRows   int                `json:"total_rows"`
	DoneRows    int                `json:"done_rows"`
	LngCol      string             `json:"lng_col"`
	LatCol      string             `json:"lat_col"`
	AddrCol     string             `json:"addr_col"`
	SpeedCol    string             `json:"speed_col"`
	Headers     []string           `json:"headers"`
	Results     []ReverseGeoResult `json:"results,omitempty"`
	CreatedAt   int64              `json:"created_at"`
	UpdatedAt   int64              `json:"updated_at"`
	FinishedAt  int64              `json:"finished_at,omitempty"`

	mu     sync.Mutex
	cancel chan struct{}
}

// ReverseGeoResult 单行逆地址转换结果
type ReverseGeoResult struct {
	Row     int    `json:"row"`
	Lng     string `json:"lng"`
	Lat     string `json:"lat"`
	Speed   string `json:"speed,omitempty"`
	Address string `json:"address"`
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

// ReverseGeoTaskView 不含 Mutex 的只读视图，用于 JSON 序列化
type ReverseGeoTaskView struct {
	ID          string             `json:"id"`
	FileName    string             `json:"file_name"`
	FilePath    string             `json:"file_path"`
	OutputDir   string             `json:"output_dir"`
	OutputFiles []string           `json:"output_files,omitempty"`
	Status      ReverseGeoStatus   `json:"status"`
	Error       string             `json:"error,omitempty"`
	TotalRows   int                `json:"total_rows"`
	DoneRows    int                `json:"done_rows"`
	LngCol      string             `json:"lng_col"`
	LatCol      string             `json:"lat_col"`
	AddrCol     string             `json:"addr_col"`
	SpeedCol    string             `json:"speed_col"`
	Headers     []string           `json:"headers"`
	Results     []ReverseGeoResult `json:"results,omitempty"`
	CreatedAt   int64              `json:"created_at"`
	UpdatedAt   int64              `json:"updated_at"`
	FinishedAt  int64              `json:"finished_at,omitempty"`
}

// View 返回只读视图(线程安全)
func (t *ReverseGeoTask) View() ReverseGeoTaskView {
	t.mu.Lock()
	defer t.mu.Unlock()
	return ReverseGeoTaskView{
		ID:          t.ID,
		FileName:    t.FileName,
		FilePath:    t.FilePath,
		OutputDir:   t.OutputDir,
		OutputFiles: t.OutputFiles,
		Status:      t.Status,
		Error:       t.Error,
		TotalRows:   t.TotalRows,
		DoneRows:    t.DoneRows,
		LngCol:      t.LngCol,
		LatCol:      t.LatCol,
		AddrCol:     t.AddrCol,
		SpeedCol:    t.SpeedCol,
		Headers:     t.Headers,
		Results:     t.Results,
		CreatedAt:   t.CreatedAt,
		UpdatedAt:   t.UpdatedAt,
		FinishedAt:  t.FinishedAt,
	}
}

// Snapshot 返回任务快照(线程安全) — 已废弃，使用 View()
func (t *ReverseGeoTask) Snapshot() ReverseGeoTask {
	v := t.View()
	return ReverseGeoTask{
		ID: v.ID, FileName: v.FileName, FilePath: v.FilePath,
		OutputDir: v.OutputDir, OutputFiles: v.OutputFiles,
		Status: v.Status, Error: v.Error,
		TotalRows: v.TotalRows, DoneRows: v.DoneRows,
		LngCol: v.LngCol, LatCol: v.LatCol, AddrCol: v.AddrCol, SpeedCol: v.SpeedCol,
		Headers: v.Headers, Results: v.Results,
		CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt, FinishedAt: v.FinishedAt,
	}
}

// ========== Tianditu API 响应结构 ==========

type tiandituResponse struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	GeoHash   string  `json:"geoHash"`
	IsCache   bool    `json:"isCache"`
	Address   string  `json:"address"`
}

// ========== 任务管理器 ==========

// ReverseGeoManager 逆地址转换任务管理器
type ReverseGeoManager struct {
	mu    sync.Mutex
	tasks map[string]*ReverseGeoTask
}

// NewReverseGeoManager 创建管理器
func NewReverseGeoManager() *ReverseGeoManager {
	return &ReverseGeoManager{
		tasks: make(map[string]*ReverseGeoTask),
	}
}

// CreateTask 创建任务
func (m *ReverseGeoManager) CreateTask(filePath, fileName string) *ReverseGeoTask {
	m.mu.Lock()
	defer m.mu.Unlock()

	id := fmt.Sprintf("reverse_%d", time.Now().UnixNano())
	task := &ReverseGeoTask{
		ID:        id,
		FileName:  fileName,
		FilePath:  filePath,
		Status:    ReverseGeoPending,
		CreatedAt: time.Now().Unix(),
		UpdatedAt: time.Now().Unix(),
		cancel:    make(chan struct{}),
	}
	m.tasks[id] = task
	return task
}

// GetTask 获取任务
func (m *ReverseGeoManager) GetTask(id string) (*ReverseGeoTask, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	task, ok := m.tasks[id]
	return task, ok
}

// ListTasks 列出所有任务
func (m *ReverseGeoManager) ListTasks() []*ReverseGeoTask {
	m.mu.Lock()
	defer m.mu.Unlock()
	tasks := make([]*ReverseGeoTask, 0, len(m.tasks))
	for _, t := range m.tasks {
		tasks = append(tasks, t)
	}
	return tasks
}

// DeleteTask 删除任务
func (m *ReverseGeoManager) DeleteTask(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.tasks, id)
}

// ========== CSV 解析 ==========

// ParseCSVHeaders 解析 CSV 文件，返回表头和行数据
func ParseCSVHeaders(filePath string) ([]string, [][]string, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, nil, fmt.Errorf("打开文件失败: %w", err)
	}
	defer f.Close()

	reader := csv.NewReader(f)
	allRecords, err := reader.ReadAll()
	if err != nil {
		return nil, nil, fmt.Errorf("读取CSV失败: %w", err)
	}
	if len(allRecords) < 1 {
		return nil, nil, fmt.Errorf("CSV文件为空")
	}

	headers := make([]string, len(allRecords[0]))
	for i, h := range allRecords[0] {
		headers[i] = strings.TrimSpace(h)
	}

	return headers, allRecords[1:], nil
}

// DetectColumns 自动检测经纬度列、地址列和速度列
// 返回: lngCol, latCol, addrCol, speedCol, lngIdx, latIdx, addrIdx, speedIdx
func DetectColumns(headers []string) (lngCol, latCol, addrCol, speedCol string, lngIdx, latIdx, addrIdx, speedIdx int) {
	lngIdx, latIdx, addrIdx, speedIdx = -1, -1, -1, -1
	lngNames := []string{"lng", "lon", "longitude", "经度", "long"}
	latNames := []string{"lat", "latitude", "纬度"}
	addrNames := []string{"address", "addr", "地址", "位置", "location"}
	speedNames := []string{"speed", "速度", "时速", "velocity"}

	for i, h := range headers {
		hl := strings.ToLower(h)
		for _, name := range lngNames {
			if hl == name || strings.Contains(hl, name) {
				lngIdx = i
				lngCol = h
				break
			}
		}
		for _, name := range latNames {
			if hl == name || strings.Contains(hl, name) {
				latIdx = i
				latCol = h
				break
			}
		}
		for _, name := range addrNames {
			if hl == name || strings.Contains(hl, name) {
				addrIdx = i
				addrCol = h
				break
			}
		}
		for _, name := range speedNames {
			if hl == name || strings.Contains(hl, name) {
				speedIdx = i
				speedCol = h
				break
			}
		}
	}

	return
}

// ========== Tianditu API 调用 ==========

const maxBatchSize = 20 // API 限制最多 20 个点/次

// batchCoord 批量请求中的单个坐标
type batchCoord struct {
	Longitude float64 `json:"longitude"`
	Latitude  float64 `json:"latitude"`
}

// batchRequest 批量请求结构
type batchRequest struct {
	CoordType string       `json:"coordtype"`
	Locations []batchCoord `json:"locations"`
}

// callTiandituReverseBatch 批量调用天地图逆地址解析接口（至多20个点/次）
func callTiandituReverseBatch(coords []batchCoord) ([]string, error) {
	if len(coords) == 0 {
		return nil, nil
	}

	// 记录请求详情
	locStrs := make([]string, len(coords))
	for i, c := range coords {
		locStrs[i] = fmt.Sprintf("%.6f,%.6f", c.Latitude, c.Longitude)
	}
	log.Printf("[TiandituAPI] >>> 批量请求逆地址解析: count=%d, locations=%v", len(coords), locStrs)

	reqBody := batchRequest{
		CoordType: "wgs84",
		Locations: coords,
	}
	bodyData, err := json.Marshal(reqBody)
	if err != nil {
		log.Printf("[TiandituAPI] <<< 请求序列化失败: %v", err)
		return nil, fmt.Errorf("请求序列化失败: %w", err)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Post(tiandituReverseURL, "application/json", strings.NewReader(string(bodyData)))
	if err != nil {
		log.Printf("[TiandituAPI] <<< HTTP请求失败: count=%d, err=%v", len(coords), err)
		return nil, fmt.Errorf("HTTP请求失败: %w", err)
	}
	defer resp.Body.Close()

	log.Printf("[TiandituAPI] <<< HTTP响应状态码: %d, count=%d", resp.StatusCode, len(coords))

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("[TiandituAPI] <<< 读取响应Body失败: count=%d, err=%v", len(coords), err)
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}

	log.Printf("[TiandituAPI] <<< 原始响应Body(%d bytes): %s", len(respBody), string(respBody))

	var results []tiandituResponse
	if err := json.Unmarshal(respBody, &results); err != nil {
		log.Printf("[TiandituAPI] <<< JSON解析失败: body=%s, err=%v", string(respBody), err)
		return nil, fmt.Errorf("解析响应失败: %w", err)
	}

	if len(results) != len(coords) {
		log.Printf("[TiandituAPI] <<< 返回数量不匹配: 请求%d个, 返回%d个", len(coords), len(results))
	}

	addrs := make([]string, len(results))
	for i, r := range results {
		if r.Address != "" {
			addrs[i] = r.Address
			log.Printf("[TiandituAPI] <<< [%d] 成功: location=%s -> address=%s", i, locStrs[i], r.Address)
		} else {
			log.Printf("[TiandituAPI] <<< [%d] 无地址: location=%s", i, locStrs[i])
		}
	}

	return addrs, nil
}

// parseFloat64 安全解析浮点数
func parseFloat64(s string) (float64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("空字符串")
	}
	return strconv.ParseFloat(s, 64)
}

// getSpeedFromRow 从行中提取速度值
func getSpeedFromRow(row []string, speedIdx int) string {
	if speedIdx >= 0 && speedIdx < len(row) {
		return strings.TrimSpace(row[speedIdx])
	}
	return ""
}

// csvSplitWriter 支持按行数分文件的 CSV 写入器
type csvSplitWriter struct {
	dir         string   // 输出目录
	baseName    string   // 文件基础名
	headers     []string // 表头
	rowsPerFile int      // 每个文件最大行数
	partNum     int      // 当前文件序号
	rowCount    int      // 当前文件已写行数
	f           *os.File
	w           *csv.Writer
	files       []string // 已完成的文件列表
}

func newCSVSplitWriter(dir, baseName string, headers []string, rowsPerFile int) *csvSplitWriter {
	return &csvSplitWriter{
		dir:         dir,
		baseName:    baseName,
		headers:     headers,
		rowsPerFile: rowsPerFile,
		partNum:     1,
	}
}

// WriteRow 写入一行，自动分文件
func (sw *csvSplitWriter) WriteRow(record []string) error {
	// 如果当前文件未创建或已满，创建新文件
	if sw.f == nil || sw.rowCount >= sw.rowsPerFile {
		if sw.f != nil {
			sw.w.Flush()
			sw.f.Close()
			sw.f = nil
		}
		fileName := fmt.Sprintf("%s_part%d.csv", sw.baseName, sw.partNum)
		filePath := filepath.Join(sw.dir, fileName)
		f, err := os.Create(filePath)
		if err != nil {
			return fmt.Errorf("创建文件失败: %w", err)
		}
		sw.f = f
		sw.w = csv.NewWriter(f)
		// 写入表头
		if err := sw.w.Write(sw.headers); err != nil {
			f.Close()
			return fmt.Errorf("写入表头失败: %w", err)
		}
		sw.files = append(sw.files, filePath)
		sw.partNum++
		sw.rowCount = 0
	}
	if err := sw.w.Write(record); err != nil {
		return fmt.Errorf("写入行失败: %w", err)
	}
	sw.w.Flush() // 实时刷盘
	sw.rowCount++
	return nil
}

// Close 关闭当前文件
func (sw *csvSplitWriter) Close() {
	if sw.f != nil {
		sw.w.Flush()
		sw.f.Close()
		sw.f = nil
	}
}

// GetFiles 返回所有输出文件路径
func (sw *csvSplitWriter) GetFiles() []string {
	return sw.files
}

// ========== 任务执行 ==========

// RunReverseGeoTask 执行逆地址转换任务(异步调用)
func (m *ReverseGeoManager) RunReverseGeoTask(taskID string) {
	task, ok := m.GetTask(taskID)
	if !ok {
		log.Printf("[ReverseGeo] 任务 %s 不存在", taskID)
		return
	}

	task.mu.Lock()
	task.Status = ReverseGeoRunning
	task.UpdatedAt = time.Now().Unix()
	task.mu.Unlock()

	log.Printf("[ReverseGeo] 开始执行任务 %s: %s", taskID, task.FilePath)

	// 读取CSV
	headers, rows, err := ParseCSVHeaders(task.FilePath)
	if err != nil {
		task.mu.Lock()
		task.Status = ReverseGeoFailed
		task.Error = err.Error()
		task.FinishedAt = time.Now().Unix()
		task.mu.Unlock()
		log.Printf("[ReverseGeo] 任务 %s 读取CSV失败: %v", taskID, err)
		return
	}

	lngCol, latCol, addrCol, speedCol, lngIdx, latIdx, addrIdx, speedIdx := DetectColumns(headers)

	task.mu.Lock()
	task.Headers = headers
	task.LngCol = lngCol
	task.LatCol = latCol
	task.AddrCol = addrCol
	task.SpeedCol = speedCol
	task.TotalRows = len(rows)
	task.mu.Unlock()

	if lngIdx < 0 || latIdx < 0 {
		task.mu.Lock()
		task.Status = ReverseGeoFailed
		task.Error = "CSV文件中未找到经度或纬度列（支持列名: 经度/lng/lon/longitude, 纬度/lat/latitude）"
		task.FinishedAt = time.Now().Unix()
		task.mu.Unlock()
		return
	}

	// ========== 创建输出目录和写入器 ==========
	cfg := config.Get()
	outputDir := filepath.Join(cfg.WorkDir, "reverse_geo")
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		task.mu.Lock()
		task.Status = ReverseGeoFailed
		task.Error = fmt.Sprintf("创建输出目录失败: %v", err)
		task.FinishedAt = time.Now().Unix()
		task.mu.Unlock()
		return
	}
	task.mu.Lock()
	task.OutputDir = outputDir
	task.mu.Unlock()

	baseName := strings.TrimSuffix(task.FileName, filepath.Ext(task.FileName))
	rowsPerFile := 10000

	needAddAddrCol := addrIdx < 0
	if needAddAddrCol {
		outHeaders := make([]string, len(headers)+1)
		copy(outHeaders, headers)
		outHeaders[len(headers)] = "地址"
		addrIdx = len(headers)
		headers = outHeaders
	}

	// 创建分文件写入器(实时写入)
	sw := newCSVSplitWriter(outputDir, baseName, headers, rowsPerFile)
	defer sw.Close()

	// ========== 主循环: 逐行处理 + 实时写入 ==========
	type pendingRow struct {
		row      []string
		lng      float64
		lat      float64
		speedVal string
	}
	var pending []pendingRow
	var errCount int

	flushPending := func() {
		if len(pending) == 0 {
			return
		}
		coords := make([]batchCoord, len(pending))
		for i, p := range pending {
			coords[i] = batchCoord{Longitude: p.lng, Latitude: p.lat}
		}
		log.Printf("[ReverseGeo] 批量请求逆地址解析: 批次大小=%d", len(pending))
		addrs, apiErr := callTiandituReverseBatch(coords)

		for i, p := range pending {
			result := ReverseGeoResult{
				Row:   task.DoneRows + 1,
				Lng:   fmt.Sprintf("%.6f", p.lng),
				Lat:   fmt.Sprintf("%.6f", p.lat),
				Speed: p.speedVal,
			}
			if apiErr != nil {
				result.Error = fmt.Sprintf("批量请求失败: %v", apiErr)
				log.Printf("[ReverseGeo] 行%d: 批量请求失败, lng=%.6f, lat=%.6f",
					task.DoneRows+1, p.lng, p.lat)
			} else if i < len(addrs) && addrs[i] != "" {
				result.Address = addrs[i]
				result.Success = true
				p.row[addrIdx] = addrs[i]
			} else {
				result.Error = "未获取到地址信息"
			}
			// 实时写入输出文件
			if err := sw.WriteRow(p.row); err != nil {
				log.Printf("[ReverseGeo] 写入输出文件失败: %v", err)
			}
			task.mu.Lock()
			task.DoneRows++
			task.UpdatedAt = time.Now().Unix()
			task.Results = append(task.Results, result)
			task.mu.Unlock()
		}
		pending = nil
		time.Sleep(200 * time.Millisecond)
	}

	for i, row := range rows {
		select {
		case <-task.cancel:
			log.Printf("[ReverseGeo] 任务 %s 被取消", taskID)
			sw.Close()
			task.mu.Lock()
			task.Status = ReverseGeoFailed
			task.Error = "任务已取消"
			task.FinishedAt = time.Now().Unix()
			task.mu.Unlock()
			return
		default:
		}

		outRow := make([]string, len(headers))
		copy(outRow[:min(len(row), len(headers))], row)
		for j := len(row); j < len(headers); j++ {
			outRow[j] = ""
		}

		speedVal := getSpeedFromRow(row, speedIdx)
		var existingAddr string
		if !needAddAddrCol && addrIdx < len(row) {
			existingAddr = strings.TrimSpace(row[addrIdx])
		}

		if existingAddr != "" {
			if err := sw.WriteRow(outRow); err != nil {
				log.Printf("[ReverseGeo] 写入失败: %v", err)
			}
			task.mu.Lock()
			task.DoneRows++
			task.UpdatedAt = time.Now().Unix()
			task.Results = append(task.Results, ReverseGeoResult{
				Row: i + 1, Lng: row[lngIdx], Lat: row[latIdx],
				Speed: speedVal, Address: existingAddr, Success: true,
			})
			task.mu.Unlock()
			continue
		}

		lngStr := row[lngIdx]
		latStr := row[latIdx]
		lngVal, err := parseFloat64(lngStr)
		if err != nil {
			errCount++
			if err := sw.WriteRow(outRow); err != nil {
				log.Printf("[ReverseGeo] 写入失败: %v", err)
			}
			task.mu.Lock()
			task.DoneRows++
			task.UpdatedAt = time.Now().Unix()
			task.Results = append(task.Results, ReverseGeoResult{
				Row: i + 1, Lng: lngStr, Lat: latStr,
				Speed: speedVal, Error: fmt.Sprintf("经度解析失败: %v", err),
			})
			task.mu.Unlock()
			continue
		}
		latVal, err := parseFloat64(latStr)
		if err != nil {
			errCount++
			if err := sw.WriteRow(outRow); err != nil {
				log.Printf("[ReverseGeo] 写入失败: %v", err)
			}
			task.mu.Lock()
			task.DoneRows++
			task.UpdatedAt = time.Now().Unix()
			task.Results = append(task.Results, ReverseGeoResult{
				Row: i + 1, Lng: lngStr, Lat: latStr,
				Speed: speedVal, Error: fmt.Sprintf("纬度解析失败: %v", err),
			})
			task.mu.Unlock()
			continue
		}

		pending = append(pending, pendingRow{
			row: outRow, lng: lngVal, lat: latVal, speedVal: speedVal,
		})

		if len(pending) >= maxBatchSize {
			flushPending()
			pct := int(math.Round(float64(task.DoneRows) / float64(task.TotalRows) * 100))
			log.Printf("[ReverseGeo] 任务 %s: %d/%d (%d%%)", taskID, task.DoneRows, task.TotalRows, pct)
		}
	}

	// 处理剩余批次
	flushPending()

	// 标记完成
	sw.Close()
	task.mu.Lock()
	task.OutputFiles = sw.GetFiles()
	task.Status = ReverseGeoCompleted
	task.FinishedAt = time.Now().Unix()
	task.UpdatedAt = time.Now().Unix()
	task.mu.Unlock()

	log.Printf("[ReverseGeo] 任务 %s 完成: %d/%d 行, 输出文件数=%d",
		taskID, task.DoneRows, task.TotalRows, len(task.OutputFiles))
}

// CancelTask 取消任务
func (m *ReverseGeoManager) CancelTask(id string) {
	task, ok := m.GetTask(id)
	if !ok {
		return
	}
	task.mu.Lock()
	defer task.mu.Unlock()
	if task.Status == ReverseGeoRunning || task.Status == ReverseGeoPending {
		select {
		case task.cancel <- struct{}{}:
		default:
		}
		close(task.cancel)
	}
}
