import React, { useState, useEffect } from 'react';
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
  Divider,
  Tooltip,
} from 'antd';
import {
  PlayCircleOutlined,
  PlusOutlined,
  DeleteOutlined,
  FolderOpenOutlined,
  CopyOutlined,
  ClockCircleOutlined,
  ImportOutlined,
} from '@ant-design/icons';
import dayjs from 'dayjs';
import {
  startFilterTask,
  listArchiveFiles,
  getConfig,
  queryTIDHistory,
  queryTIDsByTimeRange,
  ArchiveFileInfo,
  TIDImportItem,
} from '../api';

const { TextArea } = Input;
const { Text, Title } = Typography;
const { RangePicker } = DatePicker;

interface TIDEntry {
  tid: string;
  vin: string;
  plate_no: string;
}

const FilterTask: React.FC = () => {
  const [tidEntries, setTidEntries] = useState<TIDEntry[]>([{ tid: '', vin: '', plate_no: '' }]);
  const [timeRange, setTimeRange] = useState<[dayjs.Dayjs | null, dayjs.Dayjs | null]>([null, null]);
  const [archiveDir, setArchiveDir] = useState('./archive');
  const [archiveFile, setArchiveFile] = useState('');
  const [outputDir, setOutputDir] = useState('./output');
  const [workerCount, setWorkerCount] = useState(4);
  const [loading, setLoading] = useState(false);
  const [importLoading, setImportLoading] = useState(false);
  const [archiveFiles, setArchiveFiles] = useState<ArchiveFileInfo[]>([]);
  const [showFiles, setShowFiles] = useState(false);

  // 加载配置
  useEffect(() => {
    loadConfig();
  }, []);

  const loadConfig = async () => {
    try {
      const resp = await getConfig();
      const cfg = resp.data;
      setArchiveDir(cfg.archive_dir);
      setOutputDir(cfg.output_dir);
      setWorkerCount(cfg.worker_count);
      if (cfg.archive_file) {
        setArchiveFile(cfg.archive_file);
      }
    } catch (err) {
      // 使用默认值
    }
  };

  // 加载归档文件列表
  const loadArchiveFiles = async () => {
    try {
      const resp = await listArchiveFiles();
      setArchiveFiles(resp.data.files);
      setShowFiles(true);
    } catch (err: any) {
      message.error('加载归档文件列表失败: ' + (err.response?.data?.error || err.message));
    }
  };

  // 添加TID
  const addTID = () => {
    setTidEntries([...tidEntries, { tid: '', vin: '', plate_no: '' }]);
  };

  // 删除TID
  const removeTID = (index: number) => {
    if (tidEntries.length <= 1) return;
    setTidEntries(tidEntries.filter((_, i) => i !== index));
  };

  // 更新TID
  const updateTID = (index: number, value: string) => {
    const newEntries = [...tidEntries];
    newEntries[index] = { ...newEntries[index], tid: value };
    setTidEntries(newEntries);
  };

  // 更新VIN
  const updateVIN = (index: number, value: string) => {
    const newEntries = [...tidEntries];
    newEntries[index] = { ...newEntries[index], vin: value };
    setTidEntries(newEntries);
  };

  // 更新车牌号
  const updatePlateNo = (index: number, value: string) => {
    const newEntries = [...tidEntries];
    newEntries[index] = { ...newEntries[index], plate_no: value };
    setTidEntries(newEntries);
  };

  // 批量粘贴TID
  const handleBatchPasteTID = () => {
    const input = prompt('请输入TID列表（多个TID用逗号、空格或换行分隔）：');
    if (input) {
      const parsed = input.split(/[\n,，\s]+/).filter(t => t.trim());
      if (parsed.length > 0) {
        setTidEntries(parsed.map(t => ({ tid: t, vin: '', plate_no: '' })));
        message.success(`已导入 ${parsed.length} 个TID`);
      } else {
        message.warning('未识别到有效TID');
      }
    }
  };

  // 从绑定流水导入TID
  const handleImportFromBindLog = async () => {
    if (!timeRange[0] || !timeRange[1]) {
      message.warning('请先选择时间范围');
      return;
    }

    const start = timeRange[0].format('YYYY-MM-DD');
    const end = timeRange[1].format('YYYY-MM-DD');

    setImportLoading(true);
    try {
      const resp = await queryTIDsByTimeRange(start, end);
      const items: TIDEntry[] = resp.data.tids.map((item: TIDImportItem) => ({
        tid: item.tid,
        vin: item.vin,
        plate_no: item.plate_no,
      }));

      if (items.length === 0) {
        message.info('该时间范围内没有找到绑定的TID设备');
        setTidEntries([{ tid: '', vin: '', plate_no: '' }]);
      } else {
        setTidEntries(items);
        message.success(`已从绑定流水导入 ${items.length} 个TID设备`);
      }
    } catch (err: any) {
      message.error('导入失败: ' + (err.response?.data?.error || err.message));
    } finally {
      setImportLoading(false);
    }
  };

  // 启动过滤任务
  const handleStartFilter = async () => {
    const validTIDs = tidEntries.map(e => e.tid.trim()).filter(t => t);
    if (validTIDs.length === 0) {
      message.warning('请至少输入一个TID');
      return;
    }

    if (!timeRange[0] || !timeRange[1]) {
      message.warning('请选择开始和结束时间');
      return;
    }

    const startTime = timeRange[0].format('YYYY-MM-DD 00:00:00');
    const endTime = timeRange[1].format('YYYY-MM-DD 23:59:59');

    setLoading(true);

    try {
      const resp = await startFilterTask({
        tids: validTIDs,
        start_time: startTime,
        end_time: endTime,
        archive_dir: archiveDir,
        archive_file: archiveFile,
        output_dir: outputDir,
        worker_count: workerCount,
      });

      message.success(`过滤任务已启动！任务ID: ${resp.data.task_id}`);
    } catch (err: any) {
      message.error('启动任务失败: ' + (err.response?.data?.error || err.message));
    } finally {
      setLoading(false);
    }
  };

  // 文件列表表格列
  const fileColumns = [
    {
      title: '文件名',
      dataIndex: 'file_name',
      key: 'file_name',
    },
    {
      title: '文件大小',
      dataIndex: 'file_size',
      key: 'file_size',
      render: (size: number) => {
        if (size < 1024) return `${size} B`;
        if (size < 1024 * 1024) return `${(size / 1024).toFixed(1)} KB`;
        return `${(size / (1024 * 1024)).toFixed(2)} MB`;
      },
    },
    {
      title: '路径',
      dataIndex: 'file_path',
      key: 'file_path',
      ellipsis: true,
    },
  ];

  return (
    <div className="filter-task-panel">
      <Row gutter={24}>
        <Col xs={24} lg={14}>
          {/* TID输入 */}
          <Card
            title={
              <Space>
                <CopyOutlined />
                <span>TID设备号列表</span>
              </Space>
            }
            extra={
              <Space>
                <Button
                  icon={<ImportOutlined />}
                  onClick={handleImportFromBindLog}
                  size="small"
                  loading={importLoading}
                >
                  从绑定流水导入
                </Button>
                <Button icon={<PlusOutlined />} onClick={addTID} size="small">
                  添加
                </Button>
                <Button onClick={handleBatchPasteTID} size="small">
                  批量粘贴
                </Button>
              </Space>
            }
            style={{ marginBottom: 16 }}
          >
            <table style={{ width: '100%', borderCollapse: 'collapse' }}>
              <thead>
                <tr>
                  <th style={{ width: 40, padding: '4px', textAlign: 'center' }}>#</th>
                  <th style={{ padding: '4px', textAlign: 'left' }}>TID 设备号</th>
                  <th style={{ padding: '4px', textAlign: 'left' }}>车架号(VIN)</th>
                  <th style={{ padding: '4px', textAlign: 'left' }}>车牌号</th>
                  <th style={{ width: 40, padding: '4px', textAlign: 'center' }}>操作</th>
                </tr>
              </thead>
              <tbody>
                {tidEntries.map((entry, index) => (
                  <tr key={index}>
                    <td style={{ padding: '4px', textAlign: 'center', verticalAlign: 'top' }}>
                      <Text type="secondary">{index + 1}</Text>
                    </td>
                    <td style={{ padding: '4px' }}>
                      <Input
                        placeholder="输入TID设备号"
                        value={entry.tid}
                        onChange={(e) => updateTID(index, e.target.value)}
                        allowClear
                        style={{ fontFamily: 'monospace' }}
                      />
                    </td>
                    <td style={{ padding: '4px' }}>
                      <Input
                        placeholder="车架号"
                        value={entry.vin}
                        onChange={(e) => updateVIN(index, e.target.value)}
                        allowClear
                        style={{ fontFamily: 'monospace', fontSize: 12 }}
                      />
                    </td>
                    <td style={{ padding: '4px' }}>
                      <Input
                        placeholder="车牌号"
                        value={entry.plate_no}
                        onChange={(e) => updatePlateNo(index, e.target.value)}
                        allowClear
                        style={{ fontSize: 12 }}
                      />
                    </td>
                    <td style={{ padding: '4px', textAlign: 'center' }}>
                      <Button
                        type="text"
                        danger
                        icon={<DeleteOutlined />}
                        onClick={() => removeTID(index)}
                        disabled={tidEntries.length <= 1}
                      />
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
            <Text type="secondary" style={{ marginTop: 8, display: 'block' }}>
              共 {tidEntries.length} 个TID，有效 {tidEntries.filter(e => e.tid.trim()).length} 个
            </Text>
          </Card>
        </Col>

        <Col xs={24} lg={10}>
          {/* 时间范围 */}
          <Card
            title={
              <Space>
                <ClockCircleOutlined />
                <span>时间范围</span>
              </Space>
            }
            style={{ marginBottom: 16 }}
          >
            <Form layout="vertical">
              <Form.Item label="开始时间 - 结束时间" required>
                <RangePicker
                  showTime
                  value={timeRange}
                  onChange={(dates) => setTimeRange(dates || [null, null])}
                  style={{ width: '100%' }}
                  format="YYYY-MM-DD HH:mm:ss"
                  placeholder={['开始时间', '结束时间']}
                />
              </Form.Item>
            </Form>
            <Alert
              message="过滤规则：只保留GPS时间在选定范围内的记录"
              type="info"
              showIcon
              style={{ marginTop: 8 }}
            />
          </Card>
        </Col>
      </Row>

      <Row gutter={24}>
        <Col xs={24} lg={14}>
          {/* 目录配置 */}
          <Card
            title={
              <Space>
                <FolderOpenOutlined />
                <span>文件目录设置</span>
              </Space>
            }
            extra={
              <Button onClick={loadArchiveFiles} size="small">
                查看归档文件
              </Button>
            }
            style={{ marginBottom: 16 }}
          >
            <Form layout="vertical">
              <Form.Item label="归档数据文件目录">
                <Input
                  value={archiveDir}
                  onChange={(e) => setArchiveDir(e.target.value)}
                  placeholder="./archive"
                />
              </Form.Item>
              <Form.Item label="指定归档文件（可选）" help="留空则处理目录下所有.sql/.csv文件">
                <Input
                  value={archiveFile}
                  onChange={(e) => setArchiveFile(e.target.value)}
                  placeholder="例如: data_202401.sql"
                />
              </Form.Item>
              <Form.Item label="过滤结果输出目录">
                <Input
                  value={outputDir}
                  onChange={(e) => setOutputDir(e.target.value)}
                  placeholder="./output"
                />
              </Form.Item>
            </Form>

            {/* 归档文件列表 */}
            {showFiles && (
              <div style={{ marginTop: 12 }}>
                <Divider>归档文件列表</Divider>
                {archiveFiles.length > 0 ? (
                  <Table
                    dataSource={archiveFiles}
                    columns={fileColumns}
                    rowKey="file_name"
                    size="small"
                    pagination={{ pageSize: 5, size: 'small' }}
                  />
                ) : (
                  <Text type="warning">目录中未找到归档文件</Text>
                )}
              </div>
            )}
          </Card>
        </Col>

        <Col xs={24} lg={10}>
          {/* 处理参数 */}
          <Card
            title={
              <Space>
                <PlayCircleOutlined />
                <span>处理参数</span>
              </Space>
            }
            style={{ marginBottom: 16 }}
          >
            <Form layout="vertical">
              <Form.Item
                label="并行处理线程数"
                help="建议设置为CPU核心数的1-2倍"
              >
                <InputNumber
                  value={workerCount}
                  onChange={(v) => setWorkerCount(v || 4)}
                  min={1}
                  max={64}
                  style={{ width: '100%' }}
                />
              </Form.Item>
            </Form>
          </Card>
        </Col>
      </Row>

      {/* 启动按钮 */}
      <div style={{ textAlign: 'center', marginTop: 24, marginBottom: 16 }}>
        <Button
          type="primary"
          icon={<PlayCircleOutlined />}
          onClick={handleStartFilter}
          loading={loading}
          size="large"
          style={{ height: 48, paddingLeft: 48, paddingRight: 48, fontSize: 16 }}
        >
          启动过滤任务
        </Button>
      </div>

      <Alert
        message="任务流程说明"
        description={
          <ol style={{ margin: 0, paddingLeft: 20 }}>
            <li>从归档数据文件目录中读取SQL/CSV数据文件</li>
            <li>根据输入的TID列表过滤出指定设备的数据记录</li>
            <li>将过滤后的数据导入到临时MySQL数据库</li>
            <li>从临时数据库中按TID分别导出为独立的SQL文件</li>
            <li>输出文件以 TID.sql 命名，存放在输出目录中</li>
          </ol>
        }
        type="info"
        showIcon
      />
    </div>
  );
};

export default FilterTask;
