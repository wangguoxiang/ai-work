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
  Modal,
} from 'antd';
import {
  PlayCircleOutlined,
  ImportOutlined,
  CloudDownloadOutlined,
  ReloadOutlined,
  LoadingOutlined,
  FileOutlined,
  DownloadOutlined,
  FilterOutlined,
  DatabaseOutlined,
  ClockCircleOutlined,
  DeleteOutlined,
  StopOutlined,
} from '@ant-design/icons';
import {
  listCOSFiles,
  importKongCheCSV,
  createKongChePipeline,
  listPipelines,
  stopPipelineTask,
  deletePipelineTask,
  getConfig,
  COSFileInfo,
  PipelineTask,
} from '../api';

const { Text } = Typography;

const KongCheFilter: React.FC = () => {
  const fileInputRef = useRef<HTMLInputElement>(null);

  // device id 列表 + CSV 文件路径
  const [deviceIds, setDeviceIds] = useState<string[]>([]);
  const [csvFilePath, setCsvFilePath] = useState('');
  const [importLoading, setImportLoading] = useState(false);
  const [importFileName, setImportFileName] = useState('');

  // COS 文件
  const [cosFiles, setCosFiles] = useState<COSFileInfo[]>([]);
  const [selectedCOSFiles, setSelectedCOSFiles] = useState<Set<string>>(new Set());
  const [cosLoading, setCosLoading] = useState(false);
  const [cosBaseDir, setCosBaseDir] = useState('');

  // 管道任务
  const [pipelineTasks, setPipelineTasks] = useState<PipelineTask[]>([]);
  const [taskLoading, setTaskLoading] = useState(false);

  useEffect(() => {
    // 读取控车 COS 目录前缀(与 GPS 的 base_dir 不同)
    getConfig()
      .then((r) => {
        const base = r.data.kongche_db?.cos_base_dir || '';
        setCosBaseDir(base);
        if (base) loadCOSFiles(base);
      })
      .catch(() => {});
    loadPipelines();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // 定时刷新管道任务
  useEffect(() => {
    const hasRunning = pipelineTasks.some(t =>
      t.status === 'downloading' || t.status === 'filtering' || t.status === 'importing' || t.status === 'waiting'
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

  const loadCOSFiles = async (baseDir?: string) => {
    setCosLoading(true);
    try {
      const resp = await listCOSFiles('', baseDir || cosBaseDir || undefined);
      setCosFiles(resp.data.files || []);
    } catch (err: any) {
      message.error('加载COS文件失败: ' + (err.response?.data?.error || err.message));
    } finally {
      setCosLoading(false);
    }
  };

  const handleImportClick = () => fileInputRef.current?.click();

  const handleCSVFile = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    if (!file) return;
    if (!file.name.toLowerCase().endsWith('.csv')) {
      message.error('请选择 CSV 格式文件');
      return;
    }
    setImportLoading(true);
    setImportFileName(file.name);
    try {
      const resp = await importKongCheCSV(file);
      const ids = resp.data.device_ids || [];
      if (resp.data.file_path) setCsvFilePath(resp.data.file_path);
      if (ids.length === 0) {
        message.info('CSV 文件中没有解析到 device id');
        setDeviceIds([]);
      } else {
        setDeviceIds(ids);
        message.success(`已导入 ${ids.length} 个 device id`);
      }
    } catch (err: any) {
      message.error('导入失败: ' + (err.response?.data?.error || err.message));
    } finally {
      setImportLoading(false);
      if (fileInputRef.current) fileInputRef.current.value = '';
    }
  };

  const toggleCOSFile = (key: string) => {
    const s = new Set(selectedCOSFiles);
    s.has(key) ? s.delete(key) : s.add(key);
    setSelectedCOSFiles(s);
  };

  const handleCreateTask = async () => {
    if (deviceIds.length === 0) { message.warning('请先导入 device id CSV'); return; }
    if (selectedCOSFiles.size === 0) { message.warning('请选择COS文件'); return; }
    if (!csvFilePath) { message.warning('请先导入CSV文件'); return; }

    const cosKeys = Array.from(selectedCOSFiles);
    setTaskLoading(true);
    try {
      await createKongChePipeline({
        cos_keys: cosKeys,
        device_ids: deviceIds,
        csv_path: csvFilePath,
      });
      message.success({ content: '控车管道任务已创建: 下载 → 按device id过滤 → 输出SQL → 导入MySQL', key: 'kongche_pipeline_msg', duration: 5 });
      loadPipelines();
    } catch (err: any) {
      message.destroy();
      message.error('创建控车管道任务失败: ' + (err.response?.data?.error || err.message));
    } finally {
      setTaskLoading(false);
    }
  };

  // 停止正在执行的管道任务
  const handleStopPipeline = (id: string) => {
    Modal.confirm({
      title: '停止并结束任务',
      content: '确定停止并结束该任务吗? 下载/过滤/导入过程将被终止，已处理的过滤进度会保留(可断点续传)。',
      okText: '停止并结束',
      okButtonProps: { danger: true },
      cancelText: '取消',
      onOk: async () => {
        try {
          await stopPipelineTask(id);
          message.success('已发送停止请求，任务正在结束...');
          loadPipelines();
        } catch (err: any) {
          message.error('停止失败: ' + (err.response?.data?.error || err.message));
        }
      },
    });
  };

  // 删除尚未开始的管道任务
  const handleDeletePipeline = (id: string) => {
    Modal.confirm({
      title: '删除任务',
      content: '确定删除该尚未开始的任务吗? 删除后任务将从列表中移除。',
      okText: '删除',
      okButtonProps: { danger: true },
      cancelText: '取消',
      onOk: async () => {
        try {
          await deletePipelineTask(id);
          message.success('任务已删除');
          loadPipelines();
        } catch (err: any) {
          message.error('删除失败: ' + (err.response?.data?.error || err.message));
        }
      },
    });
  };

  const getPipelineStatusTag = (status: string) => {
    const m: Record<string, { label: string; color: string }> = {
      pending: { label: '等待', color: 'default' },
      waiting: { label: '排队中', color: 'orange' },
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

  const deviceCols = [
    { title: '#', key: 'idx', width: 40, render: (_: any, __: any, i: number) => i + 1 },
    { title: 'Device ID', dataIndex: 'device_id', key: 'device_id',
      render: (v: string) => <Text style={{ fontFamily: 'monospace' }}>{v}</Text> },
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

  return (
    <div>
      <Row gutter={16}>
        <Col xs={24} lg={14}>
          {/* Device ID 列表 */}
          <Card title={<Space><ImportOutlined /><span>Device ID 列表</span></Space>}
            extra={
              <Button icon={<ImportOutlined />} onClick={handleImportClick}
                size="small" loading={importLoading}>从CSV导入</Button>
            } style={{ marginBottom: 16 }}>
            <input type="file" ref={fileInputRef} accept=".csv"
              style={{ display: 'none' }} onChange={handleCSVFile} />
            {importFileName &&
              <Alert message={`CSV: ${importFileName}`} type="info" showIcon closable
                onClose={() => setImportFileName('')} style={{ marginBottom: 8 }} />}
            {deviceIds.length === 0
              ? <Text type="secondary">暂无 device id，请通过"从CSV导入"导入控车设备列表（可在"控车设备"页查询并导出）</Text>
              : <>
                  <Table dataSource={deviceIds.map((id, i) => ({ device_id: id, key: i }))}
                    columns={deviceCols} pagination={{ pageSize: 10, size: 'small' }} size="small" />
                  <Text type="secondary" style={{ marginTop: 8, display: 'block' }}>
                    共 {deviceIds.length} 个 device id
                  </Text>
                </>}
          </Card>

          {/* COS文件 */}
          <Card title={<Space><CloudDownloadOutlined /><span>COS存储桶文件{cosBaseDir ? ` · ${cosBaseDir}/` : ''}</span></Space>}
            extra={
              <Space>
                {selectedCOSFiles.size > 0 && <Tag color="blue">已选 {selectedCOSFiles.size}</Tag>}
                <Button icon={<ReloadOutlined />} onClick={() => loadCOSFiles()} size="small" loading={cosLoading}>刷新</Button>
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
              message="流程: 下载COS压缩文件 → 按 device id 过滤数据(device_id=第1列, create_time=第3列) → 输出SQL文件 → 导入临时MySQL数据库（后台独立执行）"
              type="info" showIcon />
          </Card>

          {/* 管道任务列表 */}
          <Card title={<span>📊 管道任务进度</span>} style={{ marginBottom: 16 }}
            extra={
              <Space>
                {pipelineTasks.filter(t => t.status === 'waiting').length > 0 &&
                  <Tag color="orange">排队中: {pipelineTasks.filter(t => t.status === 'waiting').length}</Tag>}
                <Button size="small" icon={<ReloadOutlined />} onClick={loadPipelines}>刷新</Button>
              </Space>
            }>
            {pipelineTasks.length === 0
              ? <Text type="secondary">暂无管道任务</Text>
              : <div style={{ maxHeight: 600, overflow: 'auto' }}>
                  {pipelineTasks.map(task => (
                    <Card key={task.id} size="small" style={{ marginBottom: 8 }}
                      title={
                        <Space style={{ width: '100%', justifyContent: 'space-between' }}>
                          <Text style={{ fontSize: 13 }}>
                            {task.elapsed || '--'}
                            {' · '}
                            <Text type="secondary" style={{ fontSize: 11 }}>
                              {task.device_id_col ? '控车' : 'GPS'} · {task.tids?.length || 0} 个ID
                            </Text>
                          </Text>
                          {getPipelineStatusTag(task.status)}
                        </Space>
                      }>
                      {task.status === 'waiting' ? (
                        <div style={{ textAlign: 'center', padding: '12px 0' }}>
                          <ClockCircleOutlined style={{ fontSize: 24, color: '#fa8c16' }} />
                          <div style={{ marginTop: 8 }}>
                            <Text type="secondary">排队等待中，当前有 {pipelineTasks.filter(t => t.status === 'downloading' || t.status === 'filtering' || t.status === 'importing').length} 个任务正在执行</Text>
                          </div>
                        </div>
                      ) : (
                      <>
                      <div style={{ marginBottom: 10 }}>
                        <Text strong style={{ fontSize: 13 }}>整体进度 {task.progress}%</Text>
                        <Progress percent={task.progress} size="small"
                          status={task.status === 'failed' ? 'exception' : undefined} />
                      </div>

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

                      <details style={{ marginTop: 8 }}>
                        <summary style={{ cursor: 'pointer', fontSize: 12, color: '#666' }}>查看详细进度</summary>
                        <div style={{ padding: '8px 0 0 8px' }}>
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
                          {task.filter_status && (
                            <div style={{ marginBottom: 6 }}>
                              <Text type="secondary" style={{ fontSize: 11 }}>
                                过滤: 保留 <Text strong style={{ color: '#16a34a' }}>{fmtNum(task.filter_lines_kept)}</Text> / {fmtNum(task.filter_lines_raw)} 条
                                {' | '}状态: {task.filter_status}
                              </Text>
                            </div>
                          )}
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
                          {task.error && (
                            <Alert type="error" message={task.error} banner style={{ fontSize: 11, marginTop: 4 }} />
                          )}
                        </div>
                      </details>
                      </>
                      )}

                      {/* 操作: 未开始任务→删除; 执行中任务→停止并结束 */}
                      {(task.status === 'pending' || task.status === 'waiting') && (
                        <div style={{ marginTop: 8, textAlign: 'right' }}>
                          <Button size="small" danger icon={<DeleteOutlined />}
                            onClick={() => handleDeletePipeline(task.id)}>删除</Button>
                        </div>
                      )}
                      {(task.status === 'downloading' || task.status === 'filtering' || task.status === 'importing') && (
                        <div style={{ marginTop: 8, textAlign: 'right' }}>
                          <Button size="small" danger icon={<StopOutlined />}
                            onClick={() => handleStopPipeline(task.id)}>停止并结束</Button>
                        </div>
                      )}
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

export default KongCheFilter;
