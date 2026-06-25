package main

import (
	"database/sql"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

//go:embed web/*
var webFS embed.FS

type Config struct {
	MySQL struct {
		Host     string   `json:"host"`
		Port     int      `json:"port"`
		User     string   `json:"user"`
		Password string   `json:"password"`
		Database string   `json:"database"`
		Table    string   `json:"table"`
		SNTable  string   `json:"sn_table"`
		Timeout  string   `json:"timeout"`
	} `json:"mysql"`
	Server struct {
		Addr string `json:"addr"`
	} `json:"server"`
	TimezoneOffset int      `json:"timezone_offset"`
	WiredTypes     []string `json:"wired_types"`
}

type EventRow struct {
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
	bind     EventRow
	unbindTS *int64
}

type BindSegment struct {
	TID        string  `json:"tid"`
	SN         string  `json:"sn"`
	VIN        string  `json:"vin"`
	CNUM       string  `json:"cnum"`
	BindTime   *string `json:"bind_time"`
	UnbindTime *string `json:"unbind_time"`
	BindTS     int64   `json:"bind_ts"`
	UnbindTS   int64   `json:"unbind_ts"`
	SNType     string  `json:"sn_type"`
	IsWired    bool    `json:"is_wired"`
}

var identRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func loadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取配置文件 %s 失败: %w", path, err)
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("解析配置文件失败: %w", err)
	}
	if c.MySQL.Port == 0 {
		c.MySQL.Port = 3306
	}
	if c.MySQL.Table == "" {
		c.MySQL.Table = "t_bind_log"
	}
	if c.MySQL.SNTable == "" {
		c.MySQL.SNTable = "t_sn"
	}
	if c.MySQL.Timeout == "" {
		c.MySQL.Timeout = "10s"
	}
	if c.Server.Addr == "" {
		c.Server.Addr = ":8080"
	}
	if c.TimezoneOffset == 0 {
		c.TimezoneOffset = 8
	}
	return &c, nil
}

func (c *Config) dsn() string {
	return fmt.Sprintf(
		"%s:%s@tcp(%s:%d)/%s?charset=utf8mb4&parseTime=true&loc=Local&timeout=%s&allowNativePasswords=true",
		c.MySQL.User, c.MySQL.Password, c.MySQL.Host, c.MySQL.Port,
		c.MySQL.Database, c.MySQL.Timeout,
	)
}

// fetchEvents 仅用 MySQL 5.7 支持的语法:固定列、WHERE vin IN(...)、ORDER BY、标量子查询。
// 用标量子查询(而非裸 LEFT JOIN)取 t_sn.type,LIMIT 1 可避免 t_sn.sn 不唯一时行被放大。
func fetchEvents(db *sql.DB, table, snTable string, vins []string) ([]EventRow, error) {
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
	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var res []EventRow
	for rows.Next() {
		var e EventRow
		if err := rows.Scan(&e.ID, &e.TID, &e.SN, &e.VIN, &e.CNUM, &e.OpType, &e.OpTime, &e.SNType); err != nil {
			return nil, err
		}
		res = append(res, e)
	}
	return res, rows.Err()
}

// buildSegments 在内存里把流水配对成"绑定段"(替代 5.7 缺失的窗口函数)。
// 按 (vin, tid) 分组(等同于"按车辆+设备";若想以 sn 为设备标识,把分组键里的 e.TID 换成 e.SN.String 即可),
// 组内按 op_time, id 升序遍历:
//   - 绑定(0):若当前没有未解绑的段 -> 开启新段;若已有未解绑的段 -> 丢弃此重复绑定,保留较早的绑定时间
//   - 解绑(2):若当前有开着段 -> 用本时间收尾;否则孤儿解绑忽略
//   - 组切换或遍历结束:仍开着的段 -> 以 unbind=nil 落盘(至今绑定)
func buildSegments(events []EventRow) []seg {
	var segs []seg
	var open *EventRow
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

// overlaps 判断"绑定段"与查询窗口是否有交集(状态重叠语义)。
// 条件:绑定开始 <= 窗口结束 且 (未解绑 或 解绑 >= 窗口开始)
func overlaps(s seg, startTS, endTS int64) bool {
	if s.bind.OpTime > endTS {
		return false
	}
	if s.unbindTS == nil {
		return true
	}
	return *s.unbindTS >= startTS
}

func parseDateTS(s string, loc *time.Location, endOfDay bool) (int64, error) {
	t, err := time.ParseInLocation("2006-01-02", s, loc)
	if err != nil {
		return 0, err
	}
	if endOfDay {
		t = t.Add(24*time.Hour - time.Second)
	}
	return t.Unix(), nil
}

func nullStr(ns sql.NullString) string {
	if ns.Valid {
		return ns.String
	}
	return ""
}

func formatTS(ts int64, loc *time.Location) string {
	return time.Unix(ts, 0).In(loc).Format("2006-01-02 15:04:05")
}

func writeJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeJSONErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]interface{}{"error": msg})
}

// parseVINs 把用户输入拆成去重的 VIN 列表,支持换行/逗号/空格/分号分隔。
func parseVINs(raw string) []string {
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\r' || r == ' ' || r == '\t' || r == ';'
	})
	seen := make(map[string]struct{})
	var out []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	return out
}

type bindLogReq struct {
	Vins  []string `json:"vins"`
	Start string   `json:"start"`
	End   string   `json:"end"`
}

func handleBindLog(db *sql.DB, cfg *Config, loc *time.Location, wiredSet map[string]bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var vins []string
		var start, end string

		switch r.Method {
		case http.MethodPost:
			var req bindLogReq
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeJSONErr(w, http.StatusBadRequest, "解析请求体失败: "+err.Error())
				return
			}
			vins, start, end = req.Vins, req.Start, req.End
		default:
			vins = parseVINs(r.URL.Query().Get("vin"))
			start = r.URL.Query().Get("start")
			end = r.URL.Query().Get("end")
		}
		if len(vins) == 0 || start == "" || end == "" {
			writeJSONErr(w, http.StatusBadRequest, "参数 vins / start / end 不能为空")
			return
		}
		startTS, err := parseDateTS(start, loc, false)
		if err != nil {
			writeJSONErr(w, http.StatusBadRequest, "开始日期格式错误,应为 YYYY-MM-DD")
			return
		}
		endTS, err := parseDateTS(end, loc, true)
		if err != nil {
			writeJSONErr(w, http.StatusBadRequest, "结束日期格式错误,应为 YYYY-MM-DD")
			return
		}

		events, err := fetchEvents(db, cfg.MySQL.Table, cfg.MySQL.SNTable, vins)
		if err != nil {
			writeJSONErr(w, http.StatusInternalServerError, "查询数据库失败: "+err.Error())
			return
		}
		segs := buildSegments(events)

		out := make([]BindSegment, 0, len(segs))
		for _, s := range segs {
			if !overlaps(s, startTS, endTS) {
				continue
			}
			bt := formatTS(s.bind.OpTime, loc)
			snType := nullStr(s.bind.SNType)
			row := BindSegment{
				TID:      s.bind.TID,
				SN:       nullStr(s.bind.SN),
				VIN:      nullStr(s.bind.VIN),
				CNUM:     nullStr(s.bind.CNUM),
				BindTime: &bt,
				BindTS:   s.bind.OpTime,
				SNType:   snType,
				IsWired:  snType != "" && wiredSet[snType],
			}
			if s.unbindTS != nil {
				ut := formatTS(*s.unbindTS, loc)
				row.UnbindTime = &ut
				row.UnbindTS = *s.unbindTS
			}
			out = append(out, row)
		}

		writeJSON(w, http.StatusOK, map[string]interface{}{
			"vins":    vins,
			"start":   start,
			"end":     end,
			"total":   len(out),
			"results": out,
		})
	}
}

func main() {
	cfgPath := flag.String("config", "config.json", "配置文件路径")
	flag.Parse()

	cfg, err := loadConfig(*cfgPath)
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}
	loc := time.FixedZone("CST", cfg.TimezoneOffset*3600)

	db, err := sql.Open("mysql", cfg.dsn())
	if err != nil {
		log.Fatalf("打开数据库失败: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(5 * time.Minute)
	if err := db.Ping(); err != nil {
		log.Fatalf("连接数据库失败: %v", err)
	}

	mux := http.NewServeMux()
	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		log.Fatalf("加载前端资源失败: %v", err)
	}
	mux.Handle("/", http.FileServer(http.FS(sub)))

	wiredSet := make(map[string]bool, len(cfg.WiredTypes))
	for _, t := range cfg.WiredTypes {
		wiredSet[t] = true
	}
	mux.HandleFunc("/api/bindlog", handleBindLog(db, cfg, loc, wiredSet))

	log.Printf("query_bind_list 已启动: http://localhost%s (db=%s table=%s sn_table=%s wired=%d tz=UTC%+d)",
		cfg.Server.Addr, cfg.MySQL.Database, cfg.MySQL.Table, cfg.MySQL.SNTable, len(wiredSet), cfg.TimezoneOffset)
	if err := http.ListenAndServe(cfg.Server.Addr, mux); err != nil {
		log.Fatalf("HTTP 服务退出: %v", err)
	}
}
