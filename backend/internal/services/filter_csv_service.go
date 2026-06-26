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

// CSVFilterTask 单个过滤任务的状态
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

	LinesDone int64 `json:"lines_done"`
	RawLines  int64 `json:"raw_lines"`
	KeptLines int64 `json:"kept_lines"`
	FirstTS   int64 `json:"first_ts"`
	LastTS    int64 `json:"last_ts"`
	Resumed   bool  `json:"resumed"`
	Pct       int   `json:"pct"`

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
		LinesDone: t.LinesDone, RawLines: t.RawLines, KeptLines: t.KeptLines,
		FirstTS: t.FirstTS, LastTS: t.LastTS, Resumed: t.Resumed,
		Pct: t.Pct, SubmitOrder: t.SubmitOrder,
	}
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

// ========== 进度持久化 ==========

// CSVProgressFile 进度持久化文件
type CSVProgressFile struct {
	TarPath    string `json:"tar_path"`
	TarSize    int64  `json:"tar_size"`
	TarMTime   int64  `json:"tar_mtime"`
	CSVPath    string `json:"csv_path"`
	CSVHash    string `json:"csv_hash"`
	OutputPath string `json:"output_path"`
	LinesDone  int64  `json:"lines_done"`
	RawLines   int64  `json:"raw_lines"`
	KeptLines  int64  `json:"kept_lines"`
	FirstTS    int64  `json:"first_ts"`
	LastTS     int64  `json:"last_ts"`
	UpdatedAt  int64  `json:"updated_at"`
}

func csvProgressPath(outputPath string) string {
	return outputPath + ".progress.json"
}

// LoadCSVProgress 加载进度文件
func LoadCSVProgress(tarPath, csvPath, outputPath string) (*CSVProgressFile, bool) {
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

func csvTaskID(tarPath, csvPath string) string {
	return fmt.Sprintf("%x", simpleHash(tarPath+"|"+csvPath))
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

// Submit 提交新任务
func (m *CSVFilterTaskManager) Submit(tarPath, csvPath, outputPath string, restart bool, groupCancel chan struct{}) (*CSVFilterTask, error) {
	if outputPath == "" {
		outputPath = csvDefaultOutputPath(tarPath)
	}
	id := csvTaskID(tarPath, csvPath)

	m.mu.Lock()
	if existing, ok := m.tasks[id]; ok && (existing.Status == CSVStatusRunning || existing.Status == CSVStatusPending) {
		m.mu.Unlock()
		return existing, nil
	}
	if restart {
		clearCSVProgress(outputPath)
		_ = os.Remove(outputPath)
	}
	cancelCh := groupCancel
	if cancelCh == nil {
		cancelCh = make(chan struct{})
	}
	m.order++
	t := &CSVFilterTask{
		ID: id, TarPath: tarPath, CSVPath: csvPath, OutputPath: outputPath,
		Status: CSVStatusPending, StartedAt: time.Now().Unix(), UpdatedAt: time.Now().Unix(),
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
	return base + "_filtered.sql"
}

// ========== CSV 解析 ==========

// ReadCSV 读取 CSV,返回 map[tid][]CSVSegment
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
	dataStart := 0
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(all[0][0])), "tid") {
		dataStart = 1
	}
	segments := make(map[string][]CSVSegment)
	for _, row := range all[dataStart:] {
		if len(row) < 4 {
			continue
		}
		for i := range row {
			row[i] = strings.TrimSpace(row[i])
		}
		tid := row[0]
		if tid == "" {
			continue
		}
		bt, err1 := strconv.ParseInt(row[2], 10, 64)
		if err1 != nil {
			continue
		}
		ubt := int64(0)
		if row[3] != "" {
			ubt, err = strconv.ParseInt(row[3], 10, 64)
			if err != nil {
				continue
			}
		}
		segments[tid] = append(segments[tid], CSVSegment{BindTS: bt, UnbindTS: ubt})
	}
	if len(segments) == 0 {
		return nil, fmt.Errorf("CSV 中未解析到有效数据")
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

// FilterLine 解析单行,返回新行/原始数/保留数/首ts/末ts
func FilterLine(line string, segments map[string][]CSVSegment, preSkipped map[string]bool) (newLine string, lineRaw, lineKept int, firstTS, lastTS int64) {
	head, valuesPart, ok := extractValuesPart(line)
	if !ok {
		return line, 0, 0, 0, 0
	}
	tuples := splitTuples(valuesPart)
	var kept []string
	for _, t := range tuples {
		fields := tupleFields(t)
		lineRaw++
		if len(fields) <= colTimestamp {
			continue
		}
		ts, err := strconv.ParseInt(strings.TrimSpace(fields[colTimestamp]), 10, 64)
		if err != nil {
			continue
		}
		lastTS = ts
		tid := stripSQLQuotes(fields[colTID])
		if preSkipped != nil && preSkipped[tid] {
			continue
		}
		segs, exists := segments[tid]
		if !exists || !segmentOverlaps(ts, segs) {
			continue
		}
		kept = append(kept, t)
		lineKept++
		if firstTS == 0 {
			firstTS = ts
		}
	}
	if len(kept) == 0 {
		return "", lineRaw, lineKept, firstTS, lastTS
	}
	return head + " " + strings.Join(kept, ",") + ";", lineRaw, lineKept, firstTS, lastTS
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
func (m *CSVFilterTaskManager) RunTask(t *CSVFilterTask, segments map[string][]CSVSegment, prog *CSVProgressFile) {
	t.setStatus(CSVStatusRunning)
	startTime := time.Now()
	log.Printf("[CSV过滤] 开始 tar=%s csv=%s output=%s", t.TarPath, t.CSVPath, t.OutputPath)

	var resumeFrom int64
	if prog != nil {
		resumeFrom = prog.LinesDone
		log.Printf("[CSV过滤] 续传: 从第 %d 行继续(已写入 %d 行, 保留 %d 条)", resumeFrom+1, resumeFrom, prog.KeptLines)
	}

	totalSegs := 0
	for _, s := range segments {
		totalSegs += len(s)
	}
	log.Printf("[CSV过滤] %d 个 TID, %d 个时间段", len(segments), totalSegs)

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

	var outFile *os.File
	if prog != nil {
		outFile, err = os.OpenFile(t.OutputPath, os.O_WRONLY|os.O_APPEND, 0644)
	} else {
		outFile, err = os.Create(t.OutputPath)
	}
	if err != nil {
		t.setError("打开输出文件失败: " + err.Error())
		return
	}
	bw := bufio.NewWriterSize(outFile, 1<<20)

	progressSaveEvery := int64(500)
	lastSavedAt := int64(0)
	progressSaveFails := 0

	curProg := &CSVProgressFile{
		TarPath: t.TarPath, TarSize: tarStat.Size(), TarMTime: tarStat.ModTime().Unix(),
		CSVPath: t.CSVPath, OutputPath: t.OutputPath,
		LinesDone: linesDone, RawLines: rawLines, KeptLines: keptLines,
		FirstTS: firstTS, LastTS: lastTS,
	}

	type lineResult struct {
		newLine string
		raw     int
		kept    int
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
			newLine, lr, lk, fTS, lTS := FilterLine(trimmed, segments, nil)
			if fTS != 0 {
				firstTS = fTS
			}
			rawLines += int64(lr)
			keptLines += int64(lk)
			if lTS != 0 {
				lastTS = lTS
			}
			writtenLines++
			if newLine != "" {
				bw.WriteString(newLine)
				bw.WriteByte('\n')
			}
			if err := bw.Flush(); err != nil {
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
		if err := bw.Flush(); err != nil {
			log.Printf("[CSV过滤] flush 失败: %v", err)
		}
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
				nl, raw, kept, _, lTS := FilterLine(l, segments, nil)
				results[idx] = lineResult{newLine: nl, raw: raw, kept: kept, lastTS: lTS}
			}(i, line)
		}
		wg.Wait()

		if cancelled {
			return nil
		}
		for _, pr := range results {
			rawLines += int64(pr.raw)
			keptLines += int64(pr.kept)
			if pr.lastTS != 0 {
				lastTS = pr.lastTS
			}
			if pr.newLine != "" {
				if _, err := bw.WriteString(pr.newLine); err != nil {
					bw.Flush()
					return err
				}
				bw.WriteByte('\n')
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
				bw.Flush()
				outFile.Close()
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
			bw.Flush()
			outFile.Close()
			t.setError("写入输出失败: " + err.Error())
			return
		}
	}

	if cancelled || fatalErr != "" {
		bw.Flush()
		outFile.Close()
		curProg.LinesDone = totalLines
		curProg.RawLines = rawLines
		curProg.KeptLines = keptLines
		curProg.FirstTS = firstTS
		curProg.LastTS = lastTS
		_ = saveCSVProgress(curProg)
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

	if err := bw.Flush(); err != nil {
		outFile.Close()
		t.setError("flush 失败: " + err.Error())
		return
	}
	outFile.Close()
	clearCSVProgress(t.OutputPath)

	t.mu.Lock()
	t.Status = CSVStatusDone
	t.LinesDone = totalLines
	t.RawLines = rawLines
	t.KeptLines = keptLines
	t.FirstTS = firstTS
	t.LastTS = lastTS
	t.Pct = 100
	t.FinishedAt = time.Now().Unix()
	t.mu.Unlock()
	log.Printf("[CSV过滤] 完成: %d 行, 原始 %d 条, 保留 %d 条, 耗时 %s",
		totalLines, rawLines, keptLines, time.Since(startTime).Round(time.Millisecond))
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

		id := csvTaskID(p.TarPath, p.CSVPath)
		m.mu.Lock()
		if _, exists := m.tasks[id]; exists {
			m.mu.Unlock()
			return nil
		}
		t := &CSVFilterTask{
			ID: id, TarPath: p.TarPath, CSVPath: p.CSVPath, OutputPath: p.OutputPath,
			Status: CSVStatusPending, StartedAt: time.Now().Unix(), UpdatedAt: time.Now().Unix(),
			cancel:    make(chan struct{}),
			LinesDone: p.LinesDone, RawLines: p.RawLines, KeptLines: p.KeptLines,
			FirstTS: p.FirstTS, LastTS: p.LastTS, Resumed: p.LinesDone > 0,
		}
		m.tasks[id] = t
		m.mu.Unlock()
		log.Printf("[CSV过滤 恢复] 恢复任务 %s: %s (已写入 %d 行)", id, p.TarPath, p.LinesDone)

		go func(pp CSVProgressFile, tt *CSVFilterTask) {
			segs, err := ReadCSV(pp.CSVPath)
			if err != nil {
				tt.setError("CSV 解析失败: " + err.Error())
				return
			}
			m.RunTask(tt, segs, &pp)
		}(p, t)
		return nil
	})
}
