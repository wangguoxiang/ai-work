import React, { useState, useEffect, useCallback } from 'react';
import {
  Card,
  Table,
  Tag,
  Button,
  Space,
  Progress,
  message,
  Typography,
  Popconfirm,
  Tooltip,
  Empty,
  Badge,
} from 'antd';
import {
  ReloadOutlined,
  DeleteOutlined,
  PlayCircleOutlined,
  CheckCircleOutlined,
  CloseCircleOutlined,
  ClockCircleOutlined,
  SyncOutlined,
  InboxOutlined,
} from '@ant-design/icons';
import { listTasks, deleteTask, TaskStatus } from '../api';

const { Text, Title } = Typography;

const TaskList: React.FC = () => {
  const [tasks, setTasks] = useState<TaskStatus[]>([]);
  const [loading, setLoading] = useState(false);
  const [autoRefresh, setAutoRefresh] = useState(true);

  // 加载任务列表
  const loadTasks = useCallback(async () => {
    setLoading(true);
    try {
      const resp = await listTasks();
      setTasks(resp.data.tasks || []);
    } catch (err: any) {
      // 忽略加载错误
    } finally {
      setLoading(false);
    }
  }, []);

  // 初始加载
  useEffect(() => {
    loadTasks();
  }, [loadTasks]);

  // 自动刷新（每3秒刷新运行中的任务）
  useEffect(() => {
    if (!autoRefresh) return;

    const hasRunning = tasks.some(t => t.status === 'running' || t.status === 'pending');
    if (!hasRunning) return;

    const timer = setInterval(() => {
      loadTasks();
    }, 3000);

    return () => clearInterval(timer);
  }, [tasks, autoRefresh, loadTasks]);

  // 删除任务
  const handleDelete = async (taskId: string) => {
    try {
      await deleteTask(taskId);
      message.success('任务已删除');
      loadTasks();
    } catch (err: any) {
      message.error('删除失败: ' + (err.response?.data?.error || err.message));
    }
  };

  // 获取状态标签
  const getStatusTag = (status: string) => {
    const statusMap: Record<string, { color: string; icon: React.ReactNode; text: string }> = {
      pending: {
        color: 'default',
        icon: <ClockCircleOutlined />,
        text: '等待中',
      },
      running: {
        color: 'processing',
        icon: <SyncOutlined spin />,
        text: '运行中',
      },
      completed: {
        color: 'success',
        icon: <CheckCircleOutlined />,
        text: '已完成',
      },
      failed: {
        color: 'error',
        icon: <CloseCircleOutlined />,
        text: '失败',
      },
    };
    const s = statusMap[status] || { color: 'default', icon: null, text: status };
    return <Tag icon={s.icon} color={s.color}>{s.text}</Tag>;
  };

  // 进度显示
  const getProgress = (task: TaskStatus) => {
    if (task.status === 'completed') return <Progress percent={100} size="small" />;
    if (task.status === 'failed') return <Progress percent={task.progress} status="exception" size="small" />;

    const progressText = `${task.processed_files}/${task.total_files} 文件, ${task.filtered_records} 条过滤`;

    return (
      <Tooltip title={progressText}>
        <Progress
          percent={Math.round(task.progress)}
          size="small"
          status="active"
        />
      </Tooltip>
    );
  };

  // 格式化文件大小
  const formatCount = (n: number) => {
    if (n >= 10000) return `${(n / 10000).toFixed(1)}万`;
    return n.toLocaleString();
  };

  // 表格列
  const columns = [
    {
      title: '任务ID',
      dataIndex: 'task_id',
      key: 'task_id',
      width: 120,
      ellipsis: true,
      render: (id: string) => (
        <Tooltip title={id}>
          <Text code style={{ fontSize: 11 }}>{id.substring(0, 8)}...</Text>
        </Tooltip>
      ),
    },
    {
      title: '状态',
      dataIndex: 'status',
      key: 'status',
      width: 100,
      render: (status: string) => getStatusTag(status),
    },
    {
      title: '进度',
      key: 'progress',
      width: 200,
      render: (_: any, record: TaskStatus) => getProgress(record),
    },
    {
      title: 'TID数量',
      key: 'tid_count',
      width: 80,
      render: (_: any, record: TaskStatus) => (
        <Text>{record.tids?.length || 0} 个</Text>
      ),
    },
    {
      title: '时间范围',
      key: 'time_range',
      width: 220,
      render: (_: any, record: TaskStatus) => (
        <Text type="secondary" style={{ fontSize: 12 }}>
          {record.start_time} ~ {record.end_time}
        </Text>
      ),
    },
    {
      title: '过滤记录',
      key: 'filtered',
      width: 100,
      render: (_: any, record: TaskStatus) => (
        <Text strong>{formatCount(record.filtered_records)}</Text>
      ),
    },
    {
      title: '导出记录',
      key: 'exported',
      width: 100,
      render: (_: any, record: TaskStatus) => (
        <Text strong>{formatCount(record.exported_records)}</Text>
      ),
    },
    {
      title: '耗时',
      dataIndex: 'elapsed',
      key: 'elapsed',
      width: 100,
    },
    {
      title: '当前文件',
      dataIndex: 'current_file',
      key: 'current_file',
      ellipsis: true,
      width: 150,
      render: (file: string) => file ? (
        <Tooltip title={file}>
          <Text type="secondary" style={{ fontSize: 12 }}>{file}</Text>
        </Tooltip>
      ) : '—',
    },
    {
      title: '操作',
      key: 'actions',
      width: 80,
      render: (_: any, record: TaskStatus) => (
        <Popconfirm
          title="确认删除此任务？"
          onConfirm={() => handleDelete(record.task_id)}
          okText="删除"
          cancelText="取消"
        >
          <Button
            type="text"
            danger
            icon={<DeleteOutlined />}
            size="small"
          />
        </Popconfirm>
      ),
    },
  ];

  // 错误信息展开
  const expandedRowRender = (record: TaskStatus) => {
    if (record.error) {
      return (
        <div style={{ padding: 8 }}>
          <Text type="danger">错误信息: {record.error}</Text>
        </div>
      );
    }
    if (record.tids && record.tids.length > 0) {
      return (
        <div style={{ padding: 8 }}>
          <Text strong>TID列表: </Text>
          <Space wrap>
            {record.tids.map((tid, i) => (
              <Tag key={i} color="blue">{tid}</Tag>
            ))}
          </Space>
        </div>
      );
    }
    return null;
  };

  return (
    <div className="task-list-panel">
      <Card
        title={
          <Space>
            <InboxOutlined />
            <span>过滤任务列表</span>
          </Space>
        }
        extra={
          <Space>
            <Button
              icon={<ReloadOutlined />}
              onClick={loadTasks}
              loading={loading}
            >
              刷新
            </Button>
          </Space>
        }
      >
        {tasks.length > 0 ? (
          <Table
            dataSource={tasks}
            columns={columns}
            rowKey="task_id"
            size="middle"
            pagination={{ pageSize: 10, showSizeChanger: true, showTotal: (t) => `共 ${t} 个任务` }}
            expandable={{
              expandedRowRender,
              rowExpandable: (record: TaskStatus) =>
                !!(record.error || (record.tids && record.tids.length > 0)),
            }}
            summary={() => {
              const running = tasks.filter(t => t.status === 'running').length;
              const completed = tasks.filter(t => t.status === 'completed').length;
              const failed = tasks.filter(t => t.status === 'failed').length;
              const totalFiltered = tasks.reduce((s, t) => s + t.filtered_records, 0);
              const totalExported = tasks.reduce((s, t) => s + t.exported_records, 0);
              return (
                <Table.Summary.Row>
                  <Table.Summary.Cell index={0} colSpan={10}>
                    <Space>
                      <Text>总计: <Text strong>{tasks.length}</Text> 个任务</Text>
                      <Badge status="processing" text={<Text>运行中: <Text strong>{running}</Text></Text>} />
                      <Badge status="success" text={<Text>已完成: <Text strong>{completed}</Text></Text>} />
                      <Badge status="error" text={<Text>失败: <Text strong>{failed}</Text></Text>} />
                      <Text>过滤记录: <Text strong>{formatCount(totalFiltered)}</Text></Text>
                      <Text>导出记录: <Text strong>{formatCount(totalExported)}</Text></Text>
                    </Space>
                  </Table.Summary.Cell>
                </Table.Summary.Row>
              );
            }}
          />
        ) : (
          <Empty
            description={
              <Space direction="vertical" style={{ textAlign: 'center' }}>
                <Text type="secondary">暂无过滤任务</Text>
                <Text type="secondary">请在"过滤任务"页面启动新的数据过滤任务</Text>
              </Space>
            }
          />
        )}
      </Card>
    </div>
  );
};

export default TaskList;
