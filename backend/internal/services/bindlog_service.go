package services

import (
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"gps-archive-tool/internal/config"
	"gps-archive-tool/internal/models"
)

// BindLogService 设备绑定流水查询服务
// 从 t_bind_log 流水表查询绑定/解绑事件,
// 在内存中按 (vin, tid) 配对成"绑定段",再按时间窗口过滤。
type BindLogService struct {
	mu  sync.RWMutex
	db  *sql.DB
	loc *time.Location
}

// NewBindLogService 创建绑定日志服务
func NewBindLogService() *BindLogService {
	return &BindLogService{}
}

// ensureConnected 确保数据库连接
func (s *BindLogService) ensureConnected() error {
	s.mu.RLock()
	if s.db != nil {
		s.mu.RUnlock()
		return nil
	}
	s.mu.RUnlock()

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db != nil {
		return nil
	}

	cfg := config.Get()
	bc := cfg.BindLogDB
	dsn := bc.DSN()

	var err error
	s.db, err = sql.Open("mysql", dsn)
	if err != nil {
		return fmt.Errorf("打开 BindLog 数据库连接失败: %w", err)
	}
	s.db.SetMaxOpenConns(10)
	s.db.SetMaxIdleConns(2)
	s.db.SetConnMaxLifetime(5 * time.Minute)

	if err := s.db.Ping(); err != nil {
		s.db.Close()
		s.db = nil
		return fmt.Errorf("连接 BindLog 数据库失败: %w", err)
	}

	s.loc = time.FixedZone("CST", cfg.TimezoneOffset*3600)
	return nil
}

// Close 关闭连接
func (s *BindLogService) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db != nil {
		s.db.Close()
		s.db = nil
	}
}

// Reconnect 重新连接（配置变更后调用）
func (s *BindLogService) Reconnect() error {
	s.Close()
	return s.ensureConnected()
}

// --- 内部类型 ---

type eventRow struct {
	ID     int64
	TID    string
	SN     sql.NullString
	VIN    sql.NullString
	CNUM   sql.NullString
	OpType int
	OpTime int64
	SNType sql.NullString
}

type seg struct {
	bind     eventRow
	unbindTS *int64
}

var identRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// fetchEvents 查询绑定事件流水
func (s *BindLogService) fetchEvents(table, snTable string, vins []string) ([]eventRow, error) {
	if !identRe.MatchString(table) {
		return nil, fmt.Errorf("非法表名: %s", table)
	}
	if !identRe.MatchString(snTable) {
		return nil, fmt.Errorf("非法SN表名: %s", snTable)
	}
	if len(vins) == 0 {
		return nil, nil
	}

	placeholders := make([]string, len(vins))
	args := make([]interface{}, len(vins))
	for i, v := range vins {
		placeholders[i] = "?"
		args[i] = v
	}

	q := fmt.Sprintf(
		"SELECT b.id, b.tid, b.sn, b.vin, b.cnum, b.op_type, b.op_time, "+
			"(SELECT s.type FROM %s s WHERE s.sn = b.sn LIMIT 1) "+
			"FROM %s b WHERE b.vin IN (%s) AND b.op_type IN (0,2) "+
			"ORDER BY b.vin, b.tid, b.op_time, b.id",
		snTable, table, strings.Join(placeholders, ","),
	)

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var res []eventRow
	for rows.Next() {
		var e eventRow
		if err := rows.Scan(&e.ID, &e.TID, &e.SN, &e.VIN, &e.CNUM, &e.OpType, &e.OpTime, &e.SNType); err != nil {
			return nil, err
		}
		res = append(res, e)
	}
	return res, rows.Err()
}

// buildSegments 在内存里把流水配对成"绑定段"
// 按 (vin, tid) 分组,组内按 op_time, id 升序遍历:
//   - 绑定(0): 若无未解绑段 -> 开启新段; 若有 -> 丢弃重复绑定,保留较早绑定时间
//   - 解绑(2): 若有开着段 -> 收尾; 否则忽略
//   - 组切换或遍历结束: 仍开着的段 -> 以 unbind=nil 落盘(至今绑定)
func buildSegments(events []eventRow) []seg {
	var segs []seg
	var open *eventRow
	curKey := ""
	flushNull := func() {
		if open != nil {
			segs = append(segs, seg{bind: *open})
			open = nil
		}
	}
	for i := range events {
		e := &events[i]
		key := e.VIN.String + "\x00" + e.TID
		if key != curKey {
			flushNull()
			curKey = key
		}
		switch e.OpType {
		case 0:
			if open == nil {
				open = e
			}
		case 2:
			if open != nil {
				ts := e.OpTime
				segs = append(segs, seg{bind: *open, unbindTS: &ts})
				open = nil
			}
		}
	}
	flushNull()
	return segs
}

// overlaps 判断"绑定段"与查询窗口是否有交集
// 条件: 绑定开始 <= 窗口结束 且 (未解绑 或 解绑 >= 窗口开始)
func overlaps(s seg, startTS, endTS int64) bool {
	if s.bind.OpTime > endTS {
		return false
	}
	if s.unbindTS == nil {
		return true
	}
	return *s.unbindTS >= startTS
}

// parseDateTS 解析日期字符串为 Unix 时间戳
func (s *BindLogService) parseDateTS(dateStr string, endOfDay bool) (int64, error) {
	t, err := time.ParseInLocation("2006-01-02", dateStr, s.loc)
	if err != nil {
		return 0, err
	}
	if endOfDay {
		t = t.Add(24*time.Hour - time.Second)
	}
	return t.Unix(), nil
}

// formatTS 格式化时间戳
func (s *BindLogService) formatTS(ts int64) string {
	return time.Unix(ts, 0).In(s.loc).Format("2006-01-02 15:04:05")
}

// TIDWithVIN 导入的TID信息
type TIDWithVIN struct {
	TID     string `json:"tid"`
	VIN     string `json:"vin"`
	PlateNo string `json:"plate_no"`
}

// QueryTIDsByTimeRange 根据时间范围查询所有绑定的TID及关联VIN
func (s *BindLogService) QueryTIDsByTimeRange(start, end string) ([]TIDWithVIN, error) {
	if err := s.ensureConnected(); err != nil {
		return nil, err
	}

	startTS, err := s.parseDateTS(start, false)
	if err != nil {
		return nil, fmt.Errorf("开始日期格式错误,应为 YYYY-MM-DD: %w", err)
	}
	endTS, err := s.parseDateTS(end, true)
	if err != nil {
		return nil, fmt.Errorf("结束日期格式错误,应为 YYYY-MM-DD: %w", err)
	}

	cfg := config.Get()
	bc := cfg.BindLogDB
	table := bc.Table
	if table == "" {
		table = "t_bind_log"
	}

	if !identRe.MatchString(table) {
		return nil, fmt.Errorf("非法表名: %s", table)
	}

	// 查询时间范围内所有绑定事件(op_type=0)的TID和VIN
	q := fmt.Sprintf(
		"SELECT DISTINCT l.tid, l.vin FROM %s l "+
			"WHERE l.op_type = 0 AND l.op_time >= ? AND l.op_time <= ? "+
			"ORDER BY l.tid",
		table,
	)

	rows, err := s.db.Query(q, startTS, endTS)
	if err != nil {
		return nil, fmt.Errorf("查询TID列表失败: %w", err)
	}
	defer rows.Close()

	var result []TIDWithVIN
	seen := make(map[string]bool) // 去重
	for rows.Next() {
		var tid, vin string
		if err := rows.Scan(&tid, &vin); err != nil {
			return nil, err
		}
		key := tid + "|" + vin
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, TIDWithVIN{TID: tid, VIN: vin})
	}
	return result, rows.Err()
}

// nullStr sql.NullString 转 string
func nullStr(ns sql.NullString) string {
	if ns.Valid {
		return ns.String
	}
	return ""
}

// QueryBindSegments 查询绑定段
func (s *BindLogService) QueryBindSegments(req models.BindLogRequest) ([]models.BindSegment, error) {
	if err := s.ensureConnected(); err != nil {
		return nil, err
	}

	if len(req.Vins) == 0 || req.Start == "" || req.End == "" {
		return nil, fmt.Errorf("参数 vins / start / end 不能为空")
	}

	startTS, err := s.parseDateTS(req.Start, false)
	if err != nil {
		return nil, fmt.Errorf("开始日期格式错误,应为 YYYY-MM-DD: %w", err)
	}
	endTS, err := s.parseDateTS(req.End, true)
	if err != nil {
		return nil, fmt.Errorf("结束日期格式错误,应为 YYYY-MM-DD: %w", err)
	}

	cfg := config.Get()
	bc := cfg.BindLogDB
	table := bc.Table
	if table == "" {
		table = "t_bind_log"
	}
	snTable := bc.SNTable
	if snTable == "" {
		snTable = "t_sn"
	}

	events, err := s.fetchEvents(table, snTable, req.Vins)
	if err != nil {
		return nil, err
	}

	segs := buildSegments(events)

	wiredSet := make(map[string]bool, len(cfg.WiredTypes))
	for _, t := range cfg.WiredTypes {
		wiredSet[t] = true
	}

	out := make([]models.BindSegment, 0, len(segs))
	for _, seg := range segs {
		if !overlaps(seg, startTS, endTS) {
			continue
		}
		bt := s.formatTS(seg.bind.OpTime)
		snType := nullStr(seg.bind.SNType)
		row := models.BindSegment{
			TID:      seg.bind.TID,
			SN:       nullStr(seg.bind.SN),
			VIN:      nullStr(seg.bind.VIN),
			CNUM:     nullStr(seg.bind.CNUM),
			BindTime: bt,
			BindTS:   seg.bind.OpTime,
			SNType:   snType,
			IsWired:  snType != "" && wiredSet[snType],
		}
		if seg.unbindTS != nil {
			row.UnbindTime = s.formatTS(*seg.unbindTS)
			row.UnbindTS = *seg.unbindTS
		}
		out = append(out, row)
	}
	return out, nil
}
