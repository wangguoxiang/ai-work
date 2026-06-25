package main

import (
	"database/sql"
	"testing"
	"time"
)

func loc() *time.Location { return time.FixedZone("CST", 8*3600) }

func ns(s string) sql.NullString { return sql.NullString{String: s, Valid: true} }

// realEvents 复刻上一轮用窗口函数 SQL 从真实库取回的 15 条流水(同一 vin+tid),
// 用于校验"内存算法 == 窗口函数 SQL"的等价性。
func realEvents() []EventRow {
	type e struct {
		id     int64
		opType int
		opTime int64
	}
	rows := []e{
		{22740, 0, 1721011921}, {22751, 2, 1721012863},
		{22762, 0, 1721012886}, {22773, 2, 1721013069},
		{22784, 0, 1721013455}, {22795, 2, 1721013570},
		{22806, 0, 1721013706}, {22813, 2, 1721013725},
		{22821, 0, 1721013765}, {22829, 2, 1721013787},
		{22846, 0, 1721013930}, {22858, 2, 1721013940},
		{22909, 0, 1721030519}, {22928, 2, 1721030544},
		{22934, 0, 1721030620},
	}
	out := make([]EventRow, len(rows))
	for i, r := range rows {
		out[i] = EventRow{
			ID:     r.id,
			TID:    "e780ad8980f64563b040994a0dc5281f",
			SN:     ns("CD02081000"),
			VIN:    ns("GF6ZVRSH57B0XJ210"),
			OpType: r.opType,
			OpTime: r.opTime,
		}
	}
	return out
}

func TestBuildSegments_RealData(t *testing.T) {
	segs := buildSegments(realEvents())
	if len(segs) != 8 {
		t.Fatalf("真实数据应配对为 8 段,实际 %d", len(segs))
	}
	wantBinds := []int64{1721011921, 1721012886, 1721013455, 1721013706, 1721013765, 1721013930, 1721030519, 1721030620}
	wantUnbinds := []int64{1721012863, 1721013069, 1721013570, 1721013725, 1721013787, 1721013940, 1721030544, -1}
	for i, s := range segs {
		if s.bind.OpTime != wantBinds[i] {
			t.Errorf("seg[%d].bind 期望 %d 实际 %d", i, wantBinds[i], s.bind.OpTime)
		}
		if wantUnbinds[i] == -1 {
			if s.unbindTS != nil {
				t.Errorf("seg[%d] 期望未解绑(nil),实际 %d", i, *s.unbindTS)
			}
		} else if s.unbindTS == nil || *s.unbindTS != wantUnbinds[i] {
			got := int64(-1)
			if s.unbindTS != nil {
				got = *s.unbindTS
			}
			t.Errorf("seg[%d].unbind 期望 %d 实际 %d", i, wantUnbinds[i], got)
		}
	}
}

// TestMergeConsecutiveBinds 需求2:未解绑又重新绑定,合并为一段,取较早绑定时间。
func TestMergeConsecutiveBinds(t *testing.T) {
	events := []EventRow{
		{ID: 1, TID: "t1", VIN: ns("V1"), SN: ns("S1"), OpType: 0, OpTime: 100},
		{ID: 2, TID: "t1", VIN: ns("V1"), SN: ns("S1"), OpType: 0, OpTime: 200},
		{ID: 3, TID: "t1", VIN: ns("V1"), SN: ns("S1"), OpType: 0, OpTime: 300},
		{ID: 4, TID: "t1", VIN: ns("V1"), SN: ns("S1"), OpType: 2, OpTime: 400},
	}
	segs := buildSegments(events)
	if len(segs) != 1 {
		t.Fatalf("连续无解绑的重复绑定应合并为 1 段,实际 %d", len(segs))
	}
	if segs[0].bind.OpTime != 100 {
		t.Errorf("合并后绑定时间应取较早的 100,实际 %d", segs[0].bind.OpTime)
	}
	if segs[0].unbindTS == nil || *segs[0].unbindTS != 400 {
		t.Errorf("解绑时间应为 400")
	}
}

// TestMergeConsecutiveBindsThenOpen 连续绑定后无解绑,应合并并保持未解绑。
func TestMergeConsecutiveBindsThenOpen(t *testing.T) {
	events := []EventRow{
		{ID: 1, TID: "t1", VIN: ns("V1"), OpType: 0, OpTime: 100},
		{ID: 2, TID: "t1", VIN: ns("V1"), OpType: 0, OpTime: 200},
	}
	segs := buildSegments(events)
	if len(segs) != 1 || segs[0].bind.OpTime != 100 || segs[0].unbindTS != nil {
		t.Fatalf("合并后应 1 段、bind=100、unbind=nil,实际 %+v", segs)
	}
}

// TestRebindAfterUnboundIsTwoSegs 解绑后再绑定是合法的两段,不被合并。
func TestRebindAfterUnboundIsTwoSegs(t *testing.T) {
	events := []EventRow{
		{ID: 1, TID: "t1", VIN: ns("V1"), OpType: 0, OpTime: 100},
		{ID: 2, TID: "t1", VIN: ns("V1"), OpType: 2, OpTime: 150},
		{ID: 3, TID: "t1", VIN: ns("V1"), OpType: 0, OpTime: 200},
	}
	segs := buildSegments(events)
	if len(segs) != 2 {
		t.Fatalf("解绑后再绑定应为 2 段,实际 %d", len(segs))
	}
	if segs[1].bind.OpTime != 200 || segs[1].unbindTS != nil {
		t.Errorf("第二段应 bind=200 且未解绑")
	}
}

func TestOverlapsWindow2025to2026(t *testing.T) {
	l := loc()
	segs := buildSegments(realEvents())
	startTS, _ := parseDateTS("2025-01-02", l, false)
	endTS, _ := parseDateTS("2026-03-04", l, true)

	var hit []int64
	for _, s := range segs {
		if overlaps(s, startTS, endTS) {
			hit = append(hit, s.bind.OpTime)
		}
	}
	if len(hit) != 1 || hit[0] != 1721030620 {
		t.Fatalf("2025-2026 窗口应仅命中最后一段(bind=1721030620),实际 %v", hit)
	}
}

func TestParseVINs(t *testing.T) {
	got := parseVINs("GF6AAA\nLG1, LG2; GF6AAA\r\n LG3")
	want := []string{"GF6AAA", "LG1", "LG2", "LG3"}
	if len(got) != len(want) {
		t.Fatalf("期望 %d 个 VIN,实际 %d (%v)", len(want), len(got), got)
	}
	for i, v := range want {
		if got[i] != v {
			t.Errorf("parseVINs[%d] 期望 %s 实际 %s", i, v, got[i])
		}
	}
}

func TestParseDateTSBoundaries(t *testing.T) {
	l := loc()
	start, _ := parseDateTS("2025-01-02", l, false)
	end, _ := parseDateTS("2025-01-02", l, true)
	if end-start != 86399 {
		t.Errorf("endOfDay 与 startOfDay 差值应为 86399 秒,实际 %d", end-start)
	}
}
