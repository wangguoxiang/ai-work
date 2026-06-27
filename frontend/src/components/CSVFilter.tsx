import React, { useState, useEffect, useRef } from 'react';
import {
  Card,
  Input,
  Button,
  Space,
  Tag,
  message,
  Alert,
  Typography,
  Row,
  Col,
  Checkbox,
  Progress,
  Table,
  Tooltip,
  Divider,
} from 'antd';
import {
  PlayCircleOutlined,
  CloseCircleOutlined,
  ReloadOutlined,
  CheckCircleOutlined,
  LoadingOutlined,
  StopOutlined,
  ClockCircleOutlined,
  FileSearchOutlined,
  UploadOutlined,
  FileAddOutlined,
} from '@ant-design/icons';
import {
  submitCSVFilter,
  listCSVFilterTasks,
  cancelCSVFilterTask,
  uploadCSVFile,
  CSVFilterTask,
} from '../api';

const { Text } = Typography;
const { TextArea } = Input;

const STATUS_MAP: Record<string, { label: string; color: string }> = {
  pending: { label: '排队中', color: 'default' },
  running: { label: '运行中', color: 'processing' },
  done: { label: '已完成', color: 'success' },
  failed: { label: '失败/取消', color: 'error' },
  resumed: { label: '续传中', color: 'warning' },
};

function fmtTS(ts: number): string {
  if (!ts) return '-';
  const d = new Date((ts + 28800) * 1000);
  return d.toISOString().replace('T', ' ').slice(0, 19);
}

function fmtNum(n: number): string {
  return (n || 0).toLocaleString();
}

function fmtDur(start: number, end?: number): string {
  if (!start) return '-';
  const ms = ((end || Math.floor(Date.now() / 1000)) - start) * 1000;
  if (ms < 1000) return ms + 'ms';
  if (ms < 60000) return (ms / 1000).toFixed(1) + 's';
  return Math.floor(ms / 60000) + 'm' + Math.floor((ms % 60000) / 1000) + 's';
}

const CSVFilter: React.FC = () => {
  const [tarPaths, setTarPaths] = useState('');
  const [csvServerPath, setCsvServerPath] = useState('');
  const [csvFileName, setCsvFileName] = useState('');
  const [uploading, setUploading] = useState(false);
  const [restart, setRestart] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [tasks, setTasks] = useState<CSVFilterTask[]>([]);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const pollTimerRef = useRef<ReturnType<typeof setInterval> | null>(null);

  const loadTasks = async () => {
    try {
      const resp = await listCSVFilterTasks();
      setTasks(resp.data.tasks || []);
    } catch (_) {}
  };

  useEffect(() => { loadTasks(); }, []);

  useEffect(() => {
    const hasRunning = tasks.some(
      (t) => t.status === 'running' || t.status === 'pending' || t.status === 'resumed'
    );
    if (hasRunning) {
      if (!pollTimerRef.current) {
        pollTimerRef.current = setInterval(loadTasks, 1500);
      }
    } else {
      if (pollTimerRef.current) {
        clearInterval(pollTimerRef.current);
        pollTimerRef.current = null;
      }
    }
    return () => {
      if (pollTimerRef.current) {
        clearInterval(pollTimerRef.current);
        pollTimerRef.current = null;
      }
    };
  }, [tasks]);

  const handleUploadClick = () => fileInputRef.current?.click();

  const handleFileChange = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    if (!file) return;
    if (!file.name.toLowerCase().endsWith('.csv')) {
      message.error('请选择 CSV 格式文件');
      return;
    }
    setUploading(true);
    try {
      const resp = await uploadCSVFile(file);
      setCsvServerPath(resp.data.file_path);
      setCsvFileName(resp.data.file_name);
      message.success(`CSV 文件已上传: ${resp.data.file_name}`);
    } catch (err: any) {
      message.error('上传失败: ' + (err.response?.data?.error || err.message));
    } finally {
      setUploading(false);
      if (fileInputRef.current) fileInputRef.current.value = '';
    }
  };

  const clearCSV = () => { setCsvServerPath(''); setCsvFileName(''); };

  const handleSubmit = async () => {
    const tarList = [...new Set(tarPaths.split(/\r?\n/).map((s) => s.trim()).filter(Boolean))];
    if (!tarList.length) { message.warning('请填写 tar.gz 路径'); return; }
    if (!csvServerPath) { message.warning('请先上传 CSV 文件'); return; }

    setSubmitting(true);
    try {
      const resp = await submitCSVFilter({ tar_paths: tarList, csv_path: csvServerPath, restart });
      const submitted = resp.data.tasks || [];
      const resumed = submitted.filter((t) => t.resumed_from > 0);
      if (resumed.length) {
        message.info(`已提交 ${submitted.length} 个任务,其中 ${resumed.length} 个将断点续传`);
      } else {
        message.success(`已提交 ${submitted.length} 个任务`);
      }
      loadTasks();
    } catch (err: any) {
      message.error('提交失败: ' + (err.response?.data?.error || err.message));
    } finally {
      setSubmitting(false);
    }
  };

  const handleCancel = async (id: string) => {
    try {
      await cancelCSVFilterTask(id);
      message.info('已发送取消请求');
      loadTasks();
    } catch (err: any) {
      message.error('取消失败: ' + (err.message || err));
    }
  };

  const columns = [
    {
      title: '状态', dataIndex: 'status', key: 'status', width: 90,
      render: (s: string) => {
        const m = STATUS_MAP[s] || { label: s, color: 'default' };
        const icon = s === 'running' || s === 'resumed' ? <LoadingOutlined />
          : s === 'done' ? <CheckCircleOutlined />
          : s === 'failed' ? <CloseCircleOutlined /> : undefined;
        return <Tag icon={icon} color={m.color}>{m.label}</Tag>;
      },
    },
    {
      title: 'tar.gz 文件', dataIndex: 'tar_path', key: 'tar_path', ellipsis: true,
      render: (v: string) => (
        <Tooltip title={v}><Text style={{ fontFamily: 'monospace', fontSize: 12 }}>{v}</Text></Tooltip>
      ),
    },
    {
      title: '过滤进度', dataIndex: 'pct', key: 'pct', width: 100,
      render: (pct: number, record: CSVFilterTask) => {
        const p = record.status === 'done' ? 100 : pct || 0;
        return <Progress percent={p} size="small" style={{ margin: 0 }} />;
      },
    },
    {
      title: '导入进度', key: 'import_progress', width: 100,
      render: (_: any, record: CSVFilterTask) => {
        if (!record.import_status || record.import_status === '') return <Text type="secondary">-</Text>;
        if (record.import_status === 'done') return <Progress percent={100} size="small" style={{ margin: 0 }} />;
        if (record.import_status === 'failed') return <Progress percent={record.import_progress || 0} size="small" status="exception" style={{ margin: 0 }} />;
        return <Progress percent={record.import_progress || 0} size="small" status="active" style={{ margin: 0 }} />;
      },
    },
    {
      title: '保留/原始', key: 'stats', width: 130,
      render: (_: any, record: CSVFilterTask) => (
        <Text style={{ fontSize: 12 }}>
          <Text style={{ color: '#16a34a', fontWeight: 600 }}>{fmtNum(record.kept_lines)}</Text>
          / {fmtNum(record.raw_lines)}
        </Text>
      ),
    },
    {
      title: '耗时', key: 'duration', width: 80,
      render: (_: any, record: CSVFilterTask) =>
        record.status === 'pending' ? '-' : fmtDur(record.started_at, record.finished_at),
    },
    {
      title: '操作', key: 'action', width: 60,
      render: (_: any, record: CSVFilterTask) =>
        (record.status === 'running' || record.status === 'pending' || record.status === 'resumed') ? (
          <Button type="text" danger size="small" icon={<StopOutlined />}
            onClick={() => handleCancel(record.id)} />
        ) : null,
    },
  ];

  const expandedRowRender = (record: CSVFilterTask) => (
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
        {/* 导入阶段详情 */}
        {record.import_status && record.import_status !== '' && (
          <>
            <Col span={24}><Divider style={{ margin: '4px 0' }} /></Col>
            <Col span={24}>
              <Text type="secondary" style={{ fontSize: 12 }}>
                MySQL导入状态：
                {record.import_status === 'importing' && <Tag color="processing" style={{ marginLeft: 4 }}>导入中</Tag>}
                {record.import_status === 'done' && <Tag color="success" style={{ marginLeft: 4 }}>导入完成</Tag>}
                {record.import_status === 'failed' && <Tag color="error" style={{ marginLeft: 4 }}>导入失败</Tag>}
                {record.import_status === 'pending' && <Tag style={{ marginLeft: 4 }}>等待导入</Tag>}
              </Text>
            </Col>
            <Col span={12}>
              <Text type="secondary" style={{ fontSize: 12 }}>
                导入进度: <Text strong>{record.import_progress || 0}%</Text>
              </Text>
            </Col>
            <Col span={12}>
              <Text type="secondary" style={{ fontSize: 12 }}>
                已导入: <Text strong>{fmtNum(record.import_done)}</Text> / {fmtNum(record.import_total)} 条
              </Text>
            </Col>
            {record.import_error && (
              <Col span={24}><Alert type="error" message={record.import_error} banner style={{ fontSize: 12 }} /></Col>
            )}
          </>
        )}
      </Row>
    </div>
  );

  return (
    <div>
      <Row gutter={16}>
        <Col xs={24} lg={12}>
          <Card title={<Space><FileSearchOutlined /><span>CSV 过滤任务</span></Space>} style={{ marginBottom: 16 }}>
            <div style={{ marginBottom: 14 }}>
              <label style={{ fontSize: 12, color: '#6b7280', display: 'block', marginBottom: 6 }}>
                tar.gz 压缩文件路径(每行一个,支持多个文件)
              </label>
              <TextArea rows={3} value={tarPaths} onChange={(e) => setTarPaths(e.target.value)}
                placeholder={"/data/PARTITION_2023_05-1.tar.gz\n/data/PARTITION_2023_05-2.tar.gz"} />
            </div>
            <div style={{ marginBottom: 14 }}>
              <label style={{ fontSize: 12, color: '#6b7280', display: 'block', marginBottom: 6 }}>
                CSV 绑定段文件(上传到服务器 work_dir)
              </label>
              <input type="file" ref={fileInputRef} accept=".csv"
                style={{ display: 'none' }} onChange={handleFileChange} />
              {csvServerPath ? (
                <Alert type="success" showIcon
                  message={<Space><FileAddOutlined /><span>{csvFileName}</span></Space>}
                  closable onClose={clearCSV}
                  action={<Button size="small" onClick={handleUploadClick} loading={uploading}>重新上传</Button>} />
              ) : (
                <Button icon={<UploadOutlined />} onClick={handleUploadClick} loading={uploading} block>
                  选择并上传 CSV 文件
                </Button>
              )}
            </div>
            <Space wrap>
              <Button type="primary" icon={<PlayCircleOutlined />}
                onClick={handleSubmit} loading={submitting} disabled={!csvServerPath}>
                提交任务
              </Button>
              <Checkbox checked={restart} onChange={(e) => setRestart(e.target.checked)}>
                重头开始(忽略已有进度)
              </Checkbox>
            </Space>
          </Card>

          <Card title={<span>使用说明</span>} style={{ marginBottom: 16 }}>
            <div style={{ fontSize: 13, lineHeight: 1.8, color: '#6b7280' }}>
              <p><strong>功能：</strong>从 gzip 压缩的 SQL 位置数据中，按 CSV 指定的设备(TID)和时间段筛选出匹配的记录。</p>
              <p><strong>CSV 格式：</strong>列顺序为 <code>tid, sn, bind_time, unbind_time</code>，时间戳为 Unix 秒级整数。</p>
              <p><strong>时间匹配：</strong><code>bind_time ≤ timestamp &lt; unbind_time</code>（左闭右开）；unbind 为空表示至今绑定。</p>
              <p><strong>断点续传：</strong>每 500 行自动保存进度，同一 tar.gz 再次提交时自动续传。</p>
              <p><strong>支持格式：</strong>tar.gz（内含 .sql）和纯 .sql.gz 文件。</p>
            </div>
          </Card>
        </Col>

        <Col xs={24} lg={12}>
          <Card title={<Space><ClockCircleOutlined /><span>任务列表</span>{tasks.length > 0 && <Tag>{tasks.length}</Tag>}</Space>}
            extra={<Button size="small" icon={<ReloadOutlined />} onClick={loadTasks}>刷新</Button>}>
            {tasks.length === 0 ? (
              <div style={{ textAlign: 'center', padding: 28, color: '#6b7280', fontSize: 13 }}>暂无任务</div>
            ) : (
              <Table dataSource={tasks} columns={columns} rowKey="id" size="small"
                pagination={{ pageSize: 10, size: 'small' }}
                expandable={{ expandedRowRender, rowExpandable: () => true }} />
            )}
          </Card>
        </Col>
      </Row>
    </div>
  );
};

export default CSVFilter;
