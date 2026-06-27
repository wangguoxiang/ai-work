import React, { useState, useEffect, useRef } from 'react';
import {
  Card,
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
  DownloadOutlined,
  FilterOutlined,
  DatabaseOutlined,
} from '@ant-design/icons';
import {
  getConfig,
  importCSV,
  listCOSFiles,
  createPipeline,
  listPipelines,
  TIDImportItem,
  COSFileInfo,
  PipelineTask,
  FileDownloadInfo,
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

  // 管道任务（统一管理下载→过滤→导入）
  const [pipelineTasks, setPipelineTasks] = useState<PipelineTask[]>([]);
  const [taskLoading, setTaskLoading] = useState(false);
  const [workerCount, setWorkerCount] = useState(4);

  // 加载配置 & 恢复管道任务
  useEffect(() => {
    getConfig().then(r => setWorkerCount(r.data.worker_count)).catch(() => {});
    loadPipelines();
    loadCOSFiles();
  }, []);

  // 定时刷新管道任务（自动覆盖下载/过滤/导入所有阶段）
  useEffect(() => {
    const hasRunning = pipelineTasks.some(t =>
      t.status === 'downloading' || t.status === 'filtering' || t.status === 'importing'
    );
    if (!hasRunning) return;
    const timer = setInterval(loadPipelines, 2000);
    return () => clearInterval(timer);
  }, [pipelineTasks]);

  const loadPipelines = async () => {
    try {
      const resp = await listPipelines();
      setPipelineTasks(resp.data.tasks || []);
    } catch (_) {}
  };

  const loadCOSFiles = async () => {
    setCosLoading(true);
    try {
      const resp = await listCOSFiles();
      setCosFiles(resp.data.files || []);
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

  // 管道状态标签
  const getPipelineStatusTag = (status: string) => {
    const m: Record<string, { label: string; color: string }> = {
      pending: { label: '等待', color: 'default' },
      downloading: { label: '下载中', color: 'processing' },
      filtering: { label: '过滤中', color: 'processing' },
      importing: { label: '导入中', color: 'processing' },
      completed: { label: '已完成', color: 'success' },
      failed: { label: '失败', color: 'error' },
    };
    const info = m[status] || { label: status, color: 'default' };
    const icon = status === 'downloading' || status === 'filtering' || status === 'importing'
      ? <LoadingOutlined /> : undefined;
    return <Tag icon={icon} color={info.color} style={{ fontSize: 11 }}>{info.label}</Tag>;
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

    const cosKeys = Array.from(selectedCOSFiles);
    setTaskLoading(true);

    try {
      await createPipeline({
        cos_keys: cosKeys,
        tids: validTIDs,
        vins: tidEntries.map(e => e.vin).filter(Boolean),
        plate_nos: tidEntries.map(e => e.plate_no).filter(Boolean),
        csv_path: csvFilePath,
      });
      message.success({ content: '管道任务已创建: 下载 → 过滤 → 自动导入MySQL，全部在后台执行', key: 'pipeline_msg', duration: 5 });
      loadPipelines();
    } catch (err: any) {
      const detail = err.response?.data?.error || err.message;
      message.destroy();
      message.error('创建管道任务失败: ' + detail);
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
    { title: '过滤进度', dataIndex: 'pct', key: 'pct', width: 100,
      render: (pct: number, record: CSVFilterTask) => {
        const p = record.status === 'done' ? 100 : pct || 0;
        return <Progress percent={p} size="small" style={{ margin: 0 }} />;
      },
    },
    { title: '导入进度', key: 'import_progress', width: 100,
      render: (_: any, record: CSVFilterTask) => {
        if (!record.import_status || record.import_status === '') return <Text type="secondary">-</Text>;
        if (record.import_status === 'done') return <Progress percent={100} size="small" style={{ margin: 0 }} />;
        if (record.import_status === 'failed') return <Progress percent={record.import_progress || 0} size="small" status="exception" style={{ margin: 0 }} />;
        return <Progress percent={record.import_progress || 0} size="small" status="active" style={{ margin: 0 }} />;
      },
    },
    { title: '保留/原始', key: 'stats', width: 100,
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
              message="流程: 下载COS文件 → 按CSV绑定段过滤SQL → 导入到临时MySQL数据库（全部在后台独立执行，关闭页面不影响）" type="info" showIcon />
          </Card>

          {/* 管道任务列表（统一显示整体进度和子任务进度） */}
          <Card title={<span>📊 管道任务进度</span>} style={{ marginBottom: 16 }}
            extra={<Button size="small" icon={<ReloadOutlined />} onClick={loadPipelines}>刷新</Button>}>
            {pipelineTasks.length === 0
              ? <Text type="secondary">暂无管道任务</Text>
              : <div style={{ maxHeight: 600, overflow: 'auto' }}>
                  {pipelineTasks.map(task => (
                    <Card key={task.id} size="small" style={{ marginBottom: 8 }}
                      title={
                        <Space style={{ width: '100%', justifyContent: 'space-between' }}>
                          <Text style={{ fontSize: 13 }}>{task.elapsed || '--'}</Text>
                          {getPipelineStatusTag(task.status)}
                        </Space>
                      }>
                      {/* 整体进度 */}
                      <div style={{ marginBottom: 10 }}>
                        <Text strong style={{ fontSize: 13 }}>整体进度 {task.progress}%</Text>
                        <Progress percent={task.progress} size="small"
                          status={task.status === 'failed' ? 'exception' : undefined} />
                      </div>

                      {/* 三个阶段状态 */}
                      <Row gutter={16} style={{ marginBottom: 8 }}>
                        <Col span={8} style={{ textAlign: 'center' }}>
                          <DownloadOutlined style={{ fontSize: 20, color: task.download_progress >= 100 ? '#52c41a' : '#1890ff' }} />
                          <div><Text type="secondary" style={{ fontSize: 11 }}>下载</Text></div>
                          <Text style={{ fontSize: 12, fontWeight: 600 }}>{task.download_progress}%</Text>
                        </Col>
                        <Col span={8} style={{ textAlign: 'center' }}>
                          <FilterOutlined style={{ fontSize: 20, color: task.filter_progress >= 100 ? '#52c41a' : task.filter_progress > 0 ? '#1890ff' : '#d9d9d9' }} />
                          <div><Text type="secondary" style={{ fontSize: 11 }}>过滤</Text></div>
                          <Text style={{ fontSize: 12, fontWeight: 600 }}>{task.filter_progress}%</Text>
                        </Col>
                        <Col span={8} style={{ textAlign: 'center' }}>
                          <DatabaseOutlined style={{ fontSize: 20, color: task.import_progress >= 100 ? '#52c41a' : task.import_progress > 0 ? '#1890ff' : '#d9d9d9' }} />
                          <div><Text type="secondary" style={{ fontSize: 11 }}>导入MySQL</Text></div>
                          <Text style={{ fontSize: 12, fontWeight: 600 }}>{task.import_progress}%</Text>
                        </Col>
                      </Row>

                      {/* 展开详情 */}
                      <details style={{ marginTop: 8 }}>
                        <summary style={{ cursor: 'pointer', fontSize: 12, color: '#666' }}>查看详细进度</summary>
                        <div style={{ padding: '8px 0 0 8px' }}>
                          {/* 下载文件明细 */}
                          {task.downloads.length > 0 && (
                            <div style={{ marginBottom: 8 }}>
                              <Text type="secondary" style={{ fontSize: 11 }}>下载文件:</Text>
                              {task.downloads.map((f, fi) => (
                                <div key={fi} style={{ display: 'flex', alignItems: 'center', gap: 8, marginTop: 4 }}>
                                  <Text ellipsis style={{ width: 140, fontSize: 11, fontFamily: 'monospace' }}>{f.file_name}</Text>
                                  <Progress percent={f.done ? 100 : f.progress} size="small" style={{ width: 100, margin: 0 }} />
                                  <Text style={{ fontSize: 10, color: f.error ? 'red' : '#666' }}>
                                    {f.error ? '失败' : f.done ? '完成' : `${f.progress}%`}
                                  </Text>
                                </div>
                              ))}
                            </div>
                          )}
                          {/* 过滤信息 */}
                          {task.filter_status && (
                            <div style={{ marginBottom: 6 }}>
                              <Text type="secondary" style={{ fontSize: 11 }}>
                                过滤: 保留 <Text strong style={{ color: '#16a34a' }}>{fmtNum(task.filter_lines_kept)}</Text> / {fmtNum(task.filter_lines_raw)} 条
                                {' | '}状态: {task.filter_status}
                              </Text>
                            </div>
                          )}
                          {/* 导入信息 */}
                          {task.import_status && task.import_status !== '' && (
                            <div>
                              <Text type="secondary" style={{ fontSize: 11 }}>
                                导入MySQL: <Text strong>{fmtNum(task.import_done)}</Text> / {fmtNum(task.import_total)} 条
                                {' | '}状态:
                                {task.import_status === 'importing' && <Tag color="processing" style={{ marginLeft: 4, fontSize: 10 }}>导入中</Tag>}
                                {task.import_status === 'done' && <Tag color="success" style={{ marginLeft: 4, fontSize: 10 }}>完成</Tag>}
                                {task.import_status === 'failed' && <Tag color="error" style={{ marginLeft: 4, fontSize: 10 }}>失败</Tag>}
                              </Text>
                              {task.import_error && <div><Text type="danger" style={{ fontSize: 11 }}>{task.import_error}</Text></div>}
                            </div>
                          )}
                          {/* 错误信息 */}
                          {task.error && (
                            <Alert type="error" message={task.error} banner style={{ fontSize: 11, marginTop: 4 }} />
                          )}
                        </div>
                      </details>
                    </Card>
                  ))}
                </div>
            }
          </Card>
        </Col>
      </Row>
    </div>
  );
};

export default FilterTask;
