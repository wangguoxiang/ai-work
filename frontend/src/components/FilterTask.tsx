import React, { useState, useEffect, useRef } from 'react';
import {
  Card,
  Form,
  Input,
  InputNumber,
  Button,
  DatePicker,
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
  ClockCircleOutlined,
  ImportOutlined,
  CloudDownloadOutlined,
  ReloadOutlined,
  CheckCircleOutlined,
  CloseCircleOutlined,
  LoadingOutlined,
  FileOutlined,
  CopyOutlined,
} from '@ant-design/icons';
import dayjs from 'dayjs';
import {
  getConfig,
  importCSV,
  listCOSFiles,
  createCOSFilterTask,
  listTasks,
  deleteTask,
  TIDImportItem,
  COSFileInfo,
  TaskStatus as TaskStatusType,
} from '../api';

const { Text } = Typography;
const { RangePicker } = DatePicker;

interface TIDEntry {
  tid: string;
  vin: string;
  plate_no: string;
}

const FilterTask: React.FC = () => {
  // TID列表
  const [tidEntries, setTidEntries] = useState<TIDEntry[]>([]);
  const [importLoading, setImportLoading] = useState(false);
  const [importFileName, setImportFileName] = useState('');
  const fileInputRef = useRef<HTMLInputElement>(null);

  // 时间范围
  const [timeRange, setTimeRange] = useState<[dayjs.Dayjs | null, dayjs.Dayjs | null]>([null, null]);

  // COS文件
  const [cosFiles, setCosFiles] = useState<COSFileInfo[]>([]);
  const [selectedCOSFiles, setSelectedCOSFiles] = useState<Set<string>>(new Set());
  const [cosLoading, setCosLoading] = useState(false);

  // 任务
  const [taskLoading, setTaskLoading] = useState(false);
  const [tasks, setTasks] = useState<TaskStatusType[]>([]);
  const [workerCount, setWorkerCount] = useState(4);

  // 加载配置
  useEffect(() => {
    getConfig().then(r => setWorkerCount(r.data.worker_count)).catch(() => {});
    loadTasks();
    loadCOSFiles();
  }, []);

  // 定时刷新运行中的任务
  useEffect(() => {
    const hasRunning = tasks.some(t => t.status === 'running' || t.status === 'pending');
    if (!hasRunning) return;
    const timer = setInterval(loadTasks, 3000);
    return () => clearInterval(timer);
  }, [tasks]);

  const loadTasks = async () => {
    try {
      const resp = await listTasks();
      setTasks(resp.data.tasks);
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
    if (!timeRange[0] || !timeRange[1]) { message.warning('请选择时间范围'); return; }
    if (selectedCOSFiles.size === 0) { message.warning('请选择COS文件'); return; }

    setTaskLoading(true);
    try {
      await createCOSFilterTask({
        tids: validTIDs,
        vins: tidEntries.map(e => e.vin),
        plate_nos: tidEntries.map(e => e.plate_no),
        start_time: timeRange[0].format('YYYY-MM-DD 00:00:00'),
        end_time: timeRange[1].format('YYYY-MM-DD 23:59:59'),
        cos_files: Array.from(selectedCOSFiles),
      });
      message.success('任务已创建');
      loadTasks();
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

  // 展开行 - 显示详细日志
  const expandedRowRender = (record: TaskStatusType) => {
    if (!record.logs || record.logs.length === 0) {
      return <Text type="secondary">暂无日志</Text>;
    }
    return (
      <div style={{ maxHeight: 300, overflow: 'auto', background: '#f6f8fa', padding: '8px 12px', borderRadius: 4 }}>
        {record.logs.map((log, i) => (
          <div key={i} style={{
            fontSize: 12, fontFamily: 'monospace',
            padding: '2px 0', color: log.includes('❌') ? '#cf1322'
              : log.includes('✅') ? '#389e0d'
              : log.includes('⬇') || log.includes('🔍') || log.includes('📝') || log.includes('💾') ? '#096dd9'
              : log.includes('⚠') ? '#d48806'
              : log.includes('  ↓') || log.includes('  ↑') || log.includes('  🔄') ? '#666'
              : '#333',
          }}>
            {log}
          </div>
        ))}
      </div>
    );
  };

  const taskCols = [
    { title: '状态', dataIndex: 'status', key: 'status', width: 80,
      render: (s: string) =>
        s === 'completed' ? <Tag icon={<CheckCircleOutlined />} color="success">完成</Tag>
        : s === 'failed' ? <Tag icon={<CloseCircleOutlined />} color="error">失败</Tag>
        : <Tag icon={<LoadingOutlined />} color="processing">运行中</Tag> },
    { title: '阶段/日志', dataIndex: 'stage', key: 'stage', width: 200, ellipsis: true },
    { title: '进度', dataIndex: 'progress', key: 'progress', width: 140,
      render: (p: number) => <Progress percent={Math.round(p)} size="small" style={{ margin: 0 }} /> },
    { title: 'TIDs', dataIndex: 'tids', key: 'tids', width: 50,
      render: (t: string[]) => <Tag>{t?.length || 0}</Tag> },
    { title: '耗时', dataIndex: 'elapsed', key: 'elapsed', width: 70 },
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
          {/* 时间范围 */}
          <Card title={<Space><ClockCircleOutlined /><span>时间范围</span></Space>} style={{ marginBottom: 16 }}>
            <Form layout="vertical">
              <Form.Item label="开始 - 结束时间" required>
                <RangePicker showTime value={timeRange}
                  onChange={d => setTimeRange(d || [null, null])}
                  style={{ width: '100%' }} format="YYYY-MM-DD HH:mm:ss" />
              </Form.Item>
              <Form.Item label="并行线程数">
                <InputNumber value={workerCount} min={1} max={64}
                  onChange={v => setWorkerCount(v || 4)} style={{ width: '100%' }} />
              </Form.Item>
            </Form>
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
              message="流程: 下载COS文件 → TID过滤 → 导出SQL → 导入MySQL" type="info" showIcon />
          </Card>

          {/* 任务列表 */}
          <Card title={<span>任务列表</span>} style={{ marginBottom: 16 }}
            extra={<Button size="small" onClick={loadTasks}>刷新</Button>}>
            {tasks.length === 0
              ? <Text type="secondary">暂无任务</Text>
              : <Table dataSource={tasks} columns={taskCols}
                  rowKey="task_id" size="small" pagination={{ pageSize: 5 }}
                  expandable={{ expandedRowRender, rowExpandable: () => true }} />}
          </Card>
        </Col>
      </Row>
    </div>
  );
};

export default FilterTask;
