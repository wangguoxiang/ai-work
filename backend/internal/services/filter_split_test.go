package services

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// 构造一个 tuple: 19 个字段, idx2=device_id, idx18=timestamp(与 colTID=2/colTimestamp=18 对应)
func mkTuple(devID string, ts int64) string {
	fields := make([]string, 19)
	for i := range fields {
		fields[i] = "'f" + strconv.Itoa(i) + "'"
	}
	fields[2] = "'" + devID + "'"
	fields[18] = strconv.FormatInt(ts, 10)
	return "(" + strings.Join(fields, ",") + ")"
}

// 生成 tar.gz(内含 data.sql, 若干 INSERT 行, 每行混排 A/B 两个 TID 的 tuple)
func makeTarGz(t *testing.T, dir string, rows [][]string) string {
	t.Helper()
	path := filepath.Join(dir, "sample.tar.gz")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	var sb strings.Builder
	for _, line := range rows {
		sb.WriteString(line[0])
		sb.WriteString("\n")
	}
	hdr := &tar.Header{Name: "data.sql", Mode: 0644, Size: int64(sb.Len())}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(sb.String())); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func makeCSV(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "binds.csv")
	content := "tid,vin,plate_no,bind_ts,unbind_ts\n" +
		"TID-A,V1,P1,1700000000,1700009000\n" +
		"TID-B,V2,P2,1700000000,\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

// 生成两类 INSERT 行: A-only / B-only / 混合 / 时间不匹配的行(应被丢弃)
func TestSplitByTIDEndToEnd(t *testing.T) {
	dir := t.TempDir()

	rows := [][]string{
		{"INSERT INTO `t_location` VALUES " + mkTuple("TID-A", 1700000100) + ";"},                                      // A 匹配
		{"INSERT INTO `t_location` VALUES " + mkTuple("TID-B", 1700000200) + ";"},                                      // B 匹配
		{"INSERT INTO `t_location` VALUES " + mkTuple("TID-A", 1700000300) + "," + mkTuple("TID-B", 1700000400) + ";"}, // 混合行
		{"INSERT INTO `t_location` VALUES " + mkTuple("TID-A", 1699990000) + ";"},                                      // A 时间在绑定前 → 丢弃
		{"INSERT INTO `t_location` VALUES " + mkTuple("TID-C", 1700000500) + ";"},                                      // 不在CSV → 丢弃
		{"CREATE TABLE foo (id int);"}, // 非INSERT行, 原样不输出
	}
	tarPath := makeTarGz(t, dir, rows)
	csvPath := makeCSV(t, dir)

	segments, err := ReadCSV(csvPath)
	if err != nil {
		t.Fatalf("ReadCSV: %v", err)
	}
	if len(segments) != 2 {
		t.Fatalf("期望 2 个 TID, 实际 %d", len(segments))
	}

	mgr := NewCSVFilterTaskManager()

	// ---- split 模式 ----
	ftSplit, err := mgr.Submit(tarPath, csvPath, "", false, nil, SubmitOpts{SplitByTID: true})
	if err != nil {
		t.Fatalf("Submit split: %v", err)
	}
	if !ftSplit.SplitByTID {
		t.Fatal("Submit 未传递 SplitByTID")
	}
	mgr.RunTask(ftSplit, segments, nil)

	snap := ftSplit.Snapshot()
	if snap.Status != CSVStatusDone {
		t.Fatalf("split 任务未完成: status=%s err=%s", snap.Status, snap.Error)
	}
	if snap.KeptLines != 4 {
		t.Fatalf("split 保留条数期望 4, 实际 %d", snap.KeptLines)
	}
	if len(snap.OutputFiles) != 2 {
		t.Fatalf("期望 2 个输出文件, 实际 %d (%v)", len(snap.OutputFiles), snap.OutputFiles)
	}

	// 断言 A/B 文件均只含自己 TID 的 tuple 且条数正确
	expect := map[string]int{"TID-A": 2, "TID-B": 2}
	for _, p := range snap.OutputFiles {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("读输出文件: %v", err)
		}
		content := string(data)
		lines := strings.Count(content, "INSERT INTO")
		if lines != 2 {
			t.Fatalf("文件 %s 期望 2 个 INSERT 行(单独源行+混合源行), 实际 %d:\n%s", p, lines, content)
		}
		tid := ""
		if strings.Contains(p, "_TID-A.sql") {
			tid = "TID-A"
		} else if strings.Contains(p, "_TID-B.sql") {
			tid = "TID-B"
		} else {
			t.Fatalf("输出文件名不含 TID: %s", p)
		}
		cnt := strings.Count(content, "'"+tid+"'")
		if cnt != expect[tid] {
			t.Fatalf("%s 期望 %d 条, 实际 %d: %s", tid, expect[tid], cnt, content)
		}
		other := "TID-B"
		if tid == "TID-B" {
			other = "TID-A"
		}
		if strings.Contains(content, "'"+other+"'") {
			t.Fatalf("%s 文件中混入了 %s 数据", tid, other)
		}
	}

	// ---- 单文件模式(回归): 保留条数应与 split 汇总一致 ----
	ftSingle, err := mgr.Submit(tarPath, csvPath, "", true, nil)
	if err != nil {
		t.Fatalf("Submit single: %v", err)
	}
	mgr.RunTask(ftSingle, segments, nil)
	snapS := ftSingle.Snapshot()
	if snapS.Status != CSVStatusDone {
		t.Fatalf("single 任务未完成: status=%s err=%s", snapS.Status, snapS.Error)
	}
	if snapS.KeptLines != snap.KeptLines {
		t.Fatalf("单文件与split保留总数不一致: %d vs %d", snapS.KeptLines, snap.KeptLines)
	}
	if len(snapS.OutputFiles) != 1 || snapS.OutputFiles[0] != ftSingle.OutputPath {
		t.Fatalf("单文件模式 OutputFiles 异常: %v", snapS.OutputFiles)
	}

	// 删除残留进度/输出
	_ = os.Remove(csvProgressPath(ftSplit.OutputPath))
	fmt.Printf("split 输出: %v\n", snap.OutputFiles)
}

// 验证 sanitizeTID
func TestSanitizeTID(t *testing.T) {
	cases := map[string]string{
		"TID-A":       "TID-A",
		"a/b\\c:d*e?": "a_b_c_d_e_",
		"  ":          "unknown",
		"":            "unknown",
	}
	for in, want := range cases {
		if got := sanitizeTID(in); got != want {
			t.Errorf("sanitizeTID(%q) = %q, want %q", in, got, want)
		}
	}
}
