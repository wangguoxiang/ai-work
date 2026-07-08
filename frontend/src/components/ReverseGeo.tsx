import React, { useState, useEffect, useRef } from 'react';
import {
  Card,
  Button,
  Space,
  Tag,
  message,
  Alert,
  Typography,
  Row,
  Col,
  Progress,
  Table,
  Tooltip,
  Divider,
  Statistic,
  Upload,
} from 'antd';
import {
  PlayCircleOutlined,
  CloseCircleOutlined,
  ReloadOutlined,
  CheckCircleOutlined,
  LoadingOutlined,
  StopOutlined,
  ClockCircleOutlined,
  UploadOutlined,
  FileAddOutlined,
  EnvironmentOutlined,
  AimOutlined,
  InboxOutlined,
  DownloadOutlined,
} from '@ant-design/icons';
import {
  uploadReverseGeoCSV,
  startReverseGeo,
  getReverseGeoTask,
  listReverseGeoTasks,
  cancelReverseGeoTask,
  getReverseGeoDownloadUrl,
  ReverseGeoTask,
  ReverseGeoTaskSummary,
} from '../api';

const { Text, Title } = Typography;
const { Dragger } = Upload;

const STATUS_MAP: Record<string, { label: string; color: string }> = {
  pending: { label: '排队中', color: 'default' },
  running: { label: '运行中', color: 'processing' },
  completed: { label: '已完成', color: 'success' },
  failed: { label: '失败/取消', color: 'error' },
};

function fmtTS(ts: number): string {
  if (!ts) return '-';
  const d = new Date(ts * 1000);
  return d.toISOString().replace('T', ' ').slice(0, 19);
}

function fmtDur(start: number, end?: number): string {
  if (!start) return '-';
  const ms = ((end || Math.floor(Date.now() / 1000)) - start) * 1000;
  if (ms < 1000) return ms + 'ms';
  if (ms < 60000) return (ms / 1000).toFixed(1) + 's';
  return Math.floor(ms / 60000) + 'm' + Math.floor((ms % 60000) / 1000) + 's';
}

const ReverseGeo: React.FC = () => {
  const [filePath, setFilePath] = useState('');
  const [fileName, setFileName] = useState('');
  const [fileHeaders, setFileHeaders] = useState<string[]>([]);
  const [detectedLng, setDetectedLng] = useState('');
  const [detectedLat, setDetectedLat] = useState('');
  const [detectedAddr, setDetectedAddr] = useState('');
  const [detectedSpeed, setDetectedSpeed] = useState('');
  const [uploading, setUploading] = useState(false);

  const [tasks, setTasks] = useState<ReverseGeoTaskSummary[]>([]);
  const [selectedTask, setSelectedTask] = useState<ReverseGeoTask | null>(null);
  const [polling, setPolling] = useState(false);

  const pollTimerRef = useRef<ReturnType<typeof setInterval> | null>(null);

  // 加载任务列表
  const loadTasks = async () => {
    try {
      const resp = await listReverseGeoTasks();
      setTasks(resp.data.tasks || []);
    } catch (_) {}
  };

  useEffect(() => { loadTasks(); }, []);

  // 轮询运行中的任务
  useEffect(() => {
    const hasRunning = tasks.some(
      (t) => t.status === 'running' || t.status === 'pending'
    );
    if (hasRunning) {
      if (!pollTimerRef.current) {
        pollTimerRef.current = setInterval(() => {
          loadTasks();
          // 如果有关联的选中任务，也刷新
          if (selectedTask && (selectedTask.status === 'running' || selectedTask.status === 'pending')) {
            loadSelectedTask(selectedTask.id);
          }
        }, 1500);
        setPolling(true);
      }
    } else {
      if (pollTimerRef.current) {
        clearInterval(pollTimerRef.current);
        pollTimerRef.current = null;
        setPolling(false);
      }
    }
    return () => {
      if (pollTimerRef.current) {
        clearInterval(pollTimerRef.current);
        pollTimerRef.current = null;
      }
    };
  }, [tasks, selectedTask]);

  // 上传CSV文件
  const handleUpload = async (file: File) => {
    if (!file.name.toLowerCase().endsWith('.csv')) {
      message.error('请选择 CSV 格式文件');
      return false;
    }
    setUploading(true);
    try {
      const resp = await uploadReverseGeoCSV(file);
      setFilePath(resp.data.file_path);
      setFileName(resp.data.file_name);
      setFileHeaders(resp.data.headers || []);
      setDetectedLng(resp.data.detected?.lng_col || '');
      setDetectedLat(resp.data.detected?.lat_col || '');
      setDetectedAddr(resp.data.detected?.addr_col || '');
      setDetectedSpeed(resp.data.detected?.speed_col || '');
      message.success(`CSV 文件已上传: ${resp.data.file_name}`);
    } catch (err: any) {
      message.error('上传失败: ' + (err.response?.data?.error || err.message));
    } finally {
      setUploading(false);
    }
    return false;
  };

  // 启动任务
  const handleStart = async () => {
    if (!filePath) {
      message.warning('请先上传 CSV 文件');
      return;
    }
    try {
      const resp = await startReverseGeo(filePath, detectedLng, detectedLat, detectedAddr);
      message.success('逆地址转换任务已启动');
      setSelectedTask(null);
      loadTasks();
    } catch (err: any) {
      message.error('启动失败: ' + (err.response?.data?.error || err.message));
    }
  };

  // 加载选中的任务详情
  const loadSelectedTask = async (taskId: string) => {
    try {
      const resp = await getReverseGeoTask(taskId);
      setSelectedTask(resp.data.task);
    } catch (_) {}
  };

  // 查看任务详情
  const handleViewTask = (taskId: string) => {
    loadSelectedTask(taskId);
  };

  // 取消任务
  const handleCancel = async (taskId: string) => {
    try {
      await cancelReverseGeoTask(taskId);
      message.info('已发送取消请求');
      loadTasks();
    } catch (err: any) {
      message.error('取消失败: ' + (err.message || err));
    }
  };

  // 清除已上传文件
  const handleClearFile = () => {
    setFilePath('');
    setFileName('');
    setFileHeaders([]);
    setDetectedLng('');
    setDetectedLat('');
    setDetectedAddr('');
    setDetectedSpeed('');
  };

  // 任务摘要表格列
  const summaryColumns = [
    {
      title: '状态', dataIndex: 'status', key: 'status', width: 90,
      render: (s: string) => {
        const m = STATUS_MAP[s] || { label: s, color: 'default' };
        const icon = s === 'running' ? <LoadingOutlined />
          : s === 'completed' ? <CheckCircleOutlined />
          : s === 'failed' ? <CloseCircleOutlined /> : undefined;
        return <Tag icon={icon} color={m.color}>{m.label}</Tag>;
      },
    },
    {
      title: '文件名', dataIndex: 'file_name', key: 'file_name', ellipsis: true,
    },
    {
      title: '进度', dataIndex: 'pct', key: 'pct', width: 140,
      render: (pct: number, record: ReverseGeoTaskSummary) => (
        <Progress percent={record.status === 'completed' ? 100 : (pct || 0)}
          size="small" style={{ margin: 0 }}
          status={record.status === 'failed' ? 'exception' : undefined} />
      ),
    },
    {
      title: '已处理/总数', key: 'count', width: 120,
      render: (_: any, record: ReverseGeoTaskSummary) => (
        <Text style={{ fontSize: 12 }}>
          <Text strong>{record.done_rows}</Text> / {record.total_rows}
        </Text>
      ),
    },
    {
      title: '耗时', key: 'duration', width: 80,
      render: (_: any, record: ReverseGeoTaskSummary) =>
        record.status === 'pending' ? '-' : fmtDur(record.created_at, record.finished_at),
    },
    {
      title: '操作', key: 'action', width: 120,
      render: (_: any, record: ReverseGeoTaskSummary) => (
        <Space size="small">
          <Button type="link" size="small" onClick={() => handleViewTask(record.id)}>
            详情
          </Button>
          {(record.status === 'running' || record.status === 'pending') && (
            <Button type="text" danger size="small" icon={<StopOutlined />}
              onClick={() => handleCancel(record.id)} />
          )}
        </Space>
      ),
    },
  ];

  // 结果表格列
  const resultColumns = [
    { title: '行号', dataIndex: 'row', key: 'row', width: 60 },
    { title: '经度', dataIndex: 'lng', key: 'lng', width: 120 },
    { title: '纬度', dataIndex: 'lat', key: 'lat', width: 120 },
    {
      title: '速度', dataIndex: 'speed', key: 'speed', width: 80,
      render: (v: string) => v ? <Text>{v}</Text> : <Text type="secondary">-</Text>,
    },
    {
      title: '地址', dataIndex: 'address', key: 'address', ellipsis: true,
      render: (v: string, record: any) => (
        record.success
          ? <Text>{v || '-'}</Text>
          : <Text type="danger">{record.error || '失败'}</Text>
      ),
    },
    {
      title: '结果', dataIndex: 'success', key: 'success', width: 70,
      render: (v: boolean) => v
        ? <Tag color="success" icon={<CheckCircleOutlined />}>成功</Tag>
        : <Tag color="error" icon={<CloseCircleOutlined />}>失败</Tag>,
    },
  ];

  return (
    <div>
      <Row gutter={16}>
        <Col xs={24} lg={12}>
          {/* 上传区域 */}
          <Card title={<Space><EnvironmentOutlined /><span>逆地址转换</span></Space>} style={{ marginBottom: 16 }}>
            <div style={{ marginBottom: 14 }}>
              <label style={{ fontSize: 12, color: '#6b7280', display: 'block', marginBottom: 6 }}>
                上传包含经纬度的 CSV 文件
              </label>
              {filePath ? (
                <Alert type="success" showIcon
                  icon={<FileAddOutlined />}
                  message={
                    <Space direction="vertical" size={2} style={{ width: '100%' }}>
                      <Text strong>{fileName}</Text>
                      {fileHeaders.length > 0 && (
                        <Text type="secondary" style={{ fontSize: 12 }}>
                          检测列: 经度={<Tag color="blue">{detectedLng || '未检测到'}</Tag>}
                          纬度={<Tag color="green">{detectedLat || '未检测到'}</Tag>}
                          速度={<Tag color="purple">{detectedSpeed || '未检测到'}</Tag>}
                          地址={<Tag color="orange">{detectedAddr || '将自动添加'}</Tag>}
                        </Text>
                      )}
                    </Space>
                  }
                  closable onClose={handleClearFile}
                  action={
                    <Upload accept=".csv" showUploadList={false}
                      beforeUpload={handleUpload} disabled={uploading}>
                      <Button size="small" icon={<UploadOutlined />} loading={uploading}>重新上传</Button>
                    </Upload>
                  } />
              ) : (
                <Dragger
                  accept=".csv"
                  showUploadList={false}
                  beforeUpload={handleUpload}
                  disabled={uploading}
                >
                  <p className="ant-upload-drag-icon">
                    <InboxOutlined />
                  </p>
                  <p className="ant-upload-text">点击或拖拽 CSV 文件到此区域上传</p>
                  <p className="ant-upload-hint">
                    支持 CSV 格式文件，需包含经度和纬度列（支持列名: 经度/lng/lon/longitude, 纬度/lat/latitude）
                  </p>
                </Dragger>
              )}
            </div>

            <Divider />

            {fileHeaders.length > 0 && (
              <div style={{ marginBottom: 14 }}>
                <Text type="secondary" style={{ fontSize: 13, fontWeight: 500 }}>CSV 列信息</Text>
                <div style={{ marginTop: 6 }}>
                  {fileHeaders.map((h, i) => {
                    let color = 'default';
                    if (h === detectedLng) color = 'blue';
                    if (h === detectedLat) color = 'green';
                    if (h === detectedSpeed) color = 'purple';
                    if (h === detectedAddr) color = 'orange';
                    return <Tag key={i} color={color} style={{ marginBottom: 4 }}>{h}</Tag>;
                  })}
                </div>
                <Text type="secondary" style={{ fontSize: 11, display: 'block', marginTop: 4 }}>
                  蓝色=经度列  绿色=纬度列  紫色=速度列  橙色=地址列
                </Text>
              </div>
            )}

            <Divider />

            <Space wrap>
              <Button type="primary" icon={<PlayCircleOutlined />}
                onClick={handleStart} disabled={!filePath} size="large">
                开始逆地址转换
              </Button>
              <Button icon={<ReloadOutlined />} onClick={loadTasks}>
                刷新列表
              </Button>
            </Space>
          </Card>

          {/* 使用说明 */}
          <Card title={<span>使用说明</span>} style={{ marginBottom: 16 }}>
            <div style={{ fontSize: 13, lineHeight: 1.8, color: '#6b7280' }}>
              <p><strong>功能：</strong>读取 CSV 文件中的经纬度坐标，调用天地图逆地址解析接口获取详细地址信息，并将结果输出到新文件。</p>
              <p><strong>CSV 格式：</strong>需包含经度列和纬度列（支持中英文列名），地址列可选（如无地址列将自动添加）。</p>
              <p><strong>处理方式：</strong>批量合并请求（最多20个点/次），提升处理速度。输出文件保存到 <code>work/reverse_geo/</code> 目录，每 <strong>1万条</strong> 数据分一个文件。</p>
              <p><strong>坐标系统：</strong>支持 WGS-84 坐标系。</p>
              <p><strong>API 接口：</strong>基于天地图逆地址解析服务。</p>
            </div>
          </Card>
        </Col>

        <Col xs={24} lg={12}>
          {/* 任务列表 */}
          <Card title={
            <Space>
              <ClockCircleOutlined />
              <span>任务列表</span>
              {polling && <Tag color="processing" icon={<LoadingOutlined />}>轮询中</Tag>}
              {tasks.length > 0 && <Tag>{tasks.length}</Tag>}
            </Space>
          }
            extra={<Button size="small" icon={<ReloadOutlined />} onClick={loadTasks}>刷新</Button>}>
            {tasks.length === 0 ? (
              <div style={{ textAlign: 'center', padding: 28, color: '#6b7280', fontSize: 13 }}>暂无任务</div>
            ) : (
              <Table dataSource={tasks} columns={summaryColumns} rowKey="id" size="small"
                pagination={{ pageSize: 10, size: 'small' }} />
            )}
          </Card>

          {/* 任务详情 */}
          {selectedTask && (
            <Card title={
              <Space>
                <AimOutlined />
                <span>任务详情: {selectedTask.file_name}</span>
                <Tag color={STATUS_MAP[selectedTask.status]?.color}>
                  {STATUS_MAP[selectedTask.status]?.label || selectedTask.status}
                </Tag>
              </Space>
            }
              style={{ marginTop: 16 }}
              extra={
                <Button size="small" onClick={() => setSelectedTask(null)}>
                  关闭
                </Button>
              }>
              {/* 进度信息 */}
              <Row gutter={[16, 8]} style={{ marginBottom: 12 }}>
                <Col span={6}>
                  <Statistic title="总行数" value={selectedTask.total_rows} valueStyle={{ fontSize: 20 }} />
                </Col>
                <Col span={6}>
                  <Statistic title="已完成" value={selectedTask.done_rows} valueStyle={{ fontSize: 20, color: '#16a34a' }} />
                </Col>
                <Col span={6}>
                  <Statistic title="耗时" value={fmtDur(selectedTask.created_at, selectedTask.finished_at)} valueStyle={{ fontSize: 18 }} />
                </Col>
                <Col span={6}>
                  <Statistic title="输出文件" value={selectedTask.output_files?.length || 0} valueStyle={{ fontSize: 18, color: '#0958d9' }} />
                </Col>
              </Row>

              {/* 输出文件列表 */}
              {selectedTask.output_files && selectedTask.output_files.length > 0 && (
                <div style={{ marginBottom: 12 }}>
                  <Text type="secondary" style={{ fontSize: 13, fontWeight: 500 }}>输出文件（点击文件名下载）</Text>
                  <div style={{ marginTop: 4 }}>
                    {selectedTask.output_files.map((f, i) => {
                      const fileName = f.split(/[\\/]/).pop() || f;
                      return (
                        <div key={i} style={{ marginBottom: 6 }}>
                          <a
                            href={getReverseGeoDownloadUrl(f)}
                            style={{ color: '#1677ff', textDecoration: 'none', fontSize: 13 }}
                            download
                          >
                            <DownloadOutlined style={{ marginRight: 4 }} />
                            {fileName}
                          </a>
                        </div>
                      );
                    })}
                  </div>
                </div>
              )}

              <Progress
                percent={selectedTask.status === 'completed' ? 100 :
                  selectedTask.total_rows > 0
                    ? Math.round(selectedTask.done_rows / selectedTask.total_rows * 100)
                    : 0}
                status={selectedTask.status === 'failed' ? 'exception' : undefined}
                style={{ marginBottom: 12 }}
              />

              {selectedTask.error && (
                <Alert type="error" message={selectedTask.error} banner style={{ marginBottom: 12, fontSize: 12 }} />
              )}

              <Divider style={{ margin: '8px 0' }} />

              {/* 结果表格 */}
              <Text type="secondary" style={{ display: 'block', marginBottom: 8 }}>
                处理结果（仅显示最近记录）
              </Text>
              <Table dataSource={selectedTask.results || []}
                columns={resultColumns}
                rowKey="row"
                size="small"
                pagination={{ pageSize: 10, size: 'small' }}
                scroll={{ x: true }}
              />
            </Card>
          )}
        </Col>
      </Row>
    </div>
  );
};

export default ReverseGeo;
