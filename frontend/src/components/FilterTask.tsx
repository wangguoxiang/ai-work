import React, { useState, useEffect, useRef } from 'react';
import {
  Card,
  InputNumber,
  Button,
  Space,
  Tag,
  message,
  Alert,
  Table,
  Typography,
  Row,
  Col,
  Checkbox,
  Progress,
} from 'antd';
import {
  PlayCircleOutlined,
  DeleteOutlined,
  ImportOutlined,
  CloudDownloadOutlined,
  ReloadOutlined,
  CheckCircleOutlined,
  CloseCircleOutlined,
  LoadingOutlined,
  FileOutlined,
  CopyOutlined,
} from '@ant-design/icons';
import {
  getConfig,
  importCSV,
  listCOSFiles,
  submitCSVFilter,
  listCSVFilterTasks,
  TIDImportItem,
  COSFileInfo,
  CSVFilterTask,
} from '../api';

const { Text } = Typography;

interface TIDEntry {
  tid: string;
  vin: string;
  plate_no: string;
}

const FilterTask: React.FC = () => {
  // TID列表 + CSV文件路径(用于后续CSV过滤)
  const [tidEntries, setTidEntries] = useState<TIDEntry[]>([]);
  const [csvFilePath, setCsvFilePath] = useState('');
  const [importLoading, setImportLoading] = useState(false);
  const [importFileName, setImportFileName] = useState('');
  const fileInputRef = useRef<HTMLInputElement>(null);

  // COS文件
  const [cosFiles, setCosFiles] = useState<COSFileInfo[]>([]);
  const [selectedCOSFiles, setSelectedCOSFiles] = useState<Set<string>>(new Set());
  const [cosLoading, setCosLoading] = useState(false);

  // 任务
  const [taskLoading, setTaskLoading] = useState(false);
  const [csvTasks, setCsvTasks] = useState<CSVFilterTask[]>([]);
  const [workerCount, setWorkerCount] = useState(4);

  // 加载配置
  useEffect(() => {
    getConfig().then(r => setWorkerCount(r.data.worker_count)).catch(() => {});
    loadCSVFilterTasks();
    loadCOSFiles();
  }, []);

  // 定时刷新运行中的任务
  useEffect(() => {
    const hasRunning = csvTasks.some(t => t.status === 'running' || t.status === 'pending' || t.status === 'resumed');
    if (!hasRunning) return;
    const timer = setInterval(loadCSVFilterTasks, 3000);
    return () => clearInterval(timer);
  }, [csvTasks]);

  const loadCSVFilterTasks = async () => {
    try {
      const resp = await listCSVFilterTasks();
      setCsvTasks(resp.data.tasks);
    } catch (_) {}
  };

  const loadCOSFiles = async () => {
    setCosLoading(true);
    try {
      const resp = await listCOSFiles();
      setCosFiles(resp.data.files);
    } catch (err: any) {
      message.error('加载COS文件失败: ' + (err.response?.data?.error || err.message));
    } finally {
      setCosLoading(false);
    }
  };

  // CSV导入TID（直接导入，不过滤时间）
  const handleImportClick = () => {
    fileInputRef.current?.click();
  };

  const handleCSVFile = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    if (!file) return;
    setImportLoading(true);
    setImportFileName(file.name);
    try {
      const resp = await importCSV(file);
      const items = resp.data.tids.map((item: TIDImportItem) => ({
        tid: item.tid, vin: item.vin, plate_no: item.plate_no,
      }));
      // 保存CSV文件服务器路径(用于后续CSV过滤)
      if (resp.data.file_path) {
        setCsvFilePath(resp.data.file_path);
      }
      if (items.length === 0) {
        message.info('CSV文件中没有TID数据');
        setTidEntries([]);
      } else {
        setTidEntries(items);
        message.success(`已导入 ${items.length} 个TID`);
      }
    } catch (err: any) {
      message.error('导入失败: ' + (err.response?.data?.error || err.message));
    } finally {
      setImportLoading(false);
      if (fileInputRef.current) fileInputRef.current.value = '';
    }
  };

  const removeTID = (idx: number) => {
    if (tidEntries.length <= 1) return;
    setTidEntries(tidEntries.filter((_, i) => i !== idx));
  };

  const toggleCOSFile = (key: string) => {
    const s = new Set(selectedCOSFiles);
    s.has(key) ? s.delete(key) : s.add(key);
    setSelectedCOSFiles(s);
  };

  const handleCreateTask = async () => {
    const validTIDs = tidEntries.map(e => e.tid.trim()).filter(Boolean);
    if (validTIDs.length === 0) { message.warning('请先导入TID'); return; }
    if (selectedCOSFiles.size === 0) { message.warning('请选择COS文件'); return; }
    if (!csvFilePath) { message.warning('请先导入CSV文件'); return; }

    setTaskLoading(true);
    try {
      // 使用CSV过滤: 从COS下载文件 → 按CSV绑定段过滤SQL → 输出SQL文件
      await submitCSVFilter({
        cos_files: Array.from(selectedCOSFiles),
        csv_path: csvFilePath,
      });
      message.success('CSV过滤任务已创建,COS文件将自动下载并过滤');
      loadCSVFilterTasks();
    } catch (err: any) {
      message.error('创建任务失败: ' + (err.response?.data?.error || err.message));
    } finally {
      setTaskLoading(false);
    }
  };

  // 表格列定义
  const tidCols = [
    { title: '#', key: 'idx', width: 40, render: (_: any, __: any, i: number) => i + 1 },
    { title: 'TID 设备号', dataIndex: 'tid', key: 'tid' },
    { title: '车架号(VIN)', dataIndex: 'vin', key: 'vin',
      render: (v: string) => v || <Text type="secondary">-</Text> },
    { title: '车牌号', dataIndex: 'plate_no', key: 'plate_no',
      render: (p: string) => p || <Text type="secondary">-</Text> },
    { title: '', key: 'act', width: 40,
      render: (_: any, __: any, i: number) => (
        <Button type="text" danger size="small" icon={<DeleteOutlined />}
          onClick={() => removeTID(i)} disabled={tidEntries.length <= 1} />
      )},
  ];

  const cosCols = [
    { title: '', key: 'cb', width: 40,
      render: (_: any, r: COSFileInfo) => (
        <Checkbox checked={selectedCOSFiles.has(r.key)} onChange={() => toggleCOSFile(r.key)} />
      )},
    { title: '文件名', dataIndex: 'name', key: 'name',
      render: (n: string) => <Space><FileOutlined /><Text ellipsis={{ tooltip: n }}>{n}</Text></Space> },
    { title: '大小', dataIndex: 'size_str', key: 'size_str', width: 100 },
    { title: '修改时间', dataIndex: 'last_mod', key: 'last_mod', width: 170 },
  ];

  function fmtNum(n: number): string {
    return (n || 0).toLocaleString();
  }

  function fmtTS(ts: number): string {
    if (!ts) return '-';
    const d = new Date((ts + 28800) * 1000);
    return d.toISOString().replace('T', ' ').slice(0, 19);
  }

  function fmtDur(start: number, end?: number): string {
    if (!start) return '-';
    const ms = ((end || Math.floor(Date.now() / 1000)) - start) * 1000;
    if (ms < 1000) return ms + 'ms';
    if (ms < 60000) return (ms / 1000).toFixed(1) + 's';
    return Math.floor(ms / 60000) + 'm' + Math.floor((ms % 60000) / 1000) + 's';
  }

  const csvTaskCols = [
    { title: '状态', dataIndex: 'status', key: 'status', width: 90,
      render: (s: string) => {
        const m: Record<string, { label: string; color: string }> = {
          pending: { label: '排队中', color: 'default' },
          running: { label: '运行中', color: 'processing' },
          done: { label: '已完成', color: 'success' },
          failed: { label: '失败/取消', color: 'error' },
          resumed: { label: '续传中', color: 'warning' },
        };
        const info = m[s] || { label: s, color: 'default' };
        const icon = s === 'running' || s === 'resumed' ? <LoadingOutlined />
          : s === 'done' ? <CheckCircleOutlined />
          : s === 'failed' ? <CloseCircleOutlined /> : undefined;
        return <Tag icon={icon} color={info.color}>{info.label}</Tag>;
      },
    },
    { title: '文件', dataIndex: 'tar_path', key: 'tar_path', ellipsis: true, width: 180,
      render: (v: string) => <Text style={{ fontFamily: 'monospace', fontSize: 12 }}>{v || '-'}</Text> },
    { title: '进度', dataIndex: 'pct', key: 'pct', width: 110,
      render: (pct: number, record: CSVFilterTask) => {
        const p = record.status === 'done' ? 100 : pct || 0;
        return <Progress percent={p} size="small" style={{ margin: 0 }} />;
      },
    },
    { title: '保留/原始', key: 'stats', width: 110,
      render: (_: any, record: CSVFilterTask) => (
        <Text style={{ fontSize: 12 }}>
          <Text style={{ color: '#16a34a', fontWeight: 600 }}>{fmtNum(record.kept_lines)}</Text>
          / {fmtNum(record.raw_lines)}
        </Text>
      ),
    },
    { title: '耗时', key: 'duration', width: 70,
      render: (_: any, record: CSVFilterTask) =>
        record.status === 'pending' ? '-' : fmtDur(record.started_at, record.finished_at),
    },
  ];

  return (
    <div>
      <Row gutter={16}>
        <Col xs={24} lg={14}>
          {/* TID列表 */}
          <Card title={<Space><CopyOutlined /><span>TID设备号列表</span></Space>}
            extra={
              <Button icon={<ImportOutlined />} onClick={handleImportClick}
                size="small" loading={importLoading}>从CSV导入</Button>
            } style={{ marginBottom: 16 }}>
            <input type="file" ref={fileInputRef} accept=".csv"
              style={{ display: 'none' }} onChange={handleCSVFile} />
            {importFileName &&
              <Alert message={`CSV: ${importFileName}`} type="info" showIcon closable
                onClose={() => setImportFileName('')} style={{ marginBottom: 8 }} />}
            {tidEntries.length === 0
              ? <Text type="secondary">暂无TID，请通过"从CSV导入"导入绑定流水数据</Text>
              : <Table dataSource={tidEntries} columns={tidCols}
                  rowKey={(_, i) => String(i)} pagination={false} size="small" />}
            {tidEntries.length > 0 &&
              <Text type="secondary" style={{ marginTop: 8, display: 'block' }}>
                共 {tidEntries.length} 个TID
              </Text>}
          </Card>

          {/* COS文件 */}
          <Card title={<Space><CloudDownloadOutlined /><span>COS存储桶文件</span></Space>}
            extra={
              <Space>
                {selectedCOSFiles.size > 0 && <Tag color="blue">已选 {selectedCOSFiles.size}</Tag>}
                <Button icon={<ReloadOutlined />} onClick={loadCOSFiles} size="small" loading={cosLoading}>刷新</Button>
              </Space>
            } style={{ marginBottom: 16 }}>
            <Table dataSource={cosFiles} columns={cosCols} rowKey="key"
              size="small" loading={cosLoading} pagination={{ pageSize: 15, size: 'small' }}
              locale={{ emptyText: 'COS中暂无文件' }} />
          </Card>
        </Col>

        <Col xs={24} lg={10}>
          {/* 并行配置 */}
          <Card title={<span>并行配置</span>} style={{ marginBottom: 16 }}>
            <div style={{ marginBottom: 4 }}>
              <label style={{ fontSize: 12, color: '#6b7280', display: 'block', marginBottom: 6 }}>
                并行线程数
              </label>
              <InputNumber value={workerCount} min={1} max={64}
                onChange={v => setWorkerCount(v || 4)} style={{ width: '100%' }} />
            </div>
          </Card>

          {/* 执行任务 */}
          <Card title={<span>执行任务</span>} style={{ marginBottom: 16 }}>
            <div style={{ textAlign: 'center' }}>
              <Button type="primary" icon={<PlayCircleOutlined />}
                onClick={handleCreateTask} loading={taskLoading}
                size="large" style={{ height: 44, padding: '0 40px', fontSize: 15 }}>
                生成执行任务
              </Button>
            </div>
            <Alert style={{ marginTop: 12 }}
              message="流程: 下载COS文件 → 按CSV绑定段过滤SQL → 输出过滤后的SQL文件" type="info" showIcon />
          </Card>

          {/* 任务列表(CSV过滤任务) */}
          <Card title={<span>CSV过滤任务列表</span>} style={{ marginBottom: 16 }}
            extra={<Button size="small" onClick={loadCSVFilterTasks}>刷新</Button>}>
            {csvTasks.length === 0
              ? <Text type="secondary">暂无任务</Text>
              : <Table dataSource={csvTasks} columns={csvTaskCols}
                  rowKey="id" size="small" pagination={{ pageSize: 5 }}
                  expandable={{
                    expandedRowRender: (record: CSVFilterTask) => (
                      <div style={{ padding: '8px 0' }}>
                        <Row gutter={[16, 8]}>
                          <Col span={12}>
                            <Text type="secondary" style={{ fontSize: 12 }}>
                              CSV: <Text code style={{ fontSize: 11 }}>{record.csv_path}</Text>
                            </Text>
                          </Col>
                          <Col span={12}>
                            <Text type="secondary" style={{ fontSize: 12 }}>
                              输出: <Text code style={{ fontSize: 11 }}>{record.output_path}</Text>
                            </Text>
                          </Col>
                          <Col span={8}>
                            <Text type="secondary" style={{ fontSize: 12 }}>
                              已处理行: <Text strong>{fmtNum(record.lines_done)}</Text>
                            </Text>
                          </Col>
                          <Col span={8}>
                            <Text type="secondary" style={{ fontSize: 12 }}>
                              数据时间范围: <Text strong>{fmtTS(record.first_ts)} ~ {fmtTS(record.last_ts)}</Text>
                            </Text>
                          </Col>
                          <Col span={8}>
                            <Text type="secondary" style={{ fontSize: 12 }}>
                              最后更新: <Text strong>{fmtTS(record.updated_at)}</Text>
                            </Text>
                          </Col>
                          {record.resumed && (
                            <Col span={24}><Tag color="warning">断点续传</Tag></Col>
                          )}
                          {record.error && (
                            <Col span={24}><Alert type="error" message={record.error} banner style={{ fontSize: 12 }} /></Col>
                          )}
                        </Row>
                      </div>
                    ),
                    rowExpandable: () => true,
                  }} />}
          </Card>
        </Col>
      </Row>
    </div>
  );
};

export default FilterTask;
