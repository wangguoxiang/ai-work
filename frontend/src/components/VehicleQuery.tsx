import React, { useState } from 'react';
import {
  Card,
  Input,
  Button,
  Table,
  Tag,
  Space,
  Typography,
  Divider,
  Alert,
  Spin,
  Descriptions,
  Timeline,
  message,
  Tooltip,
} from 'antd';
import {
  SearchOutlined,
  PlusOutlined,
  DeleteOutlined,
  HistoryOutlined,
  CheckCircleOutlined,
  CloseCircleOutlined,
  ReloadOutlined,
} from '@ant-design/icons';
import { queryVehicle, batchQueryVehicle, VehicleQueryResult, VehicleInfo } from '../api';

const { TextArea } = Input;
const { Title, Text } = Typography;

const VehicleQuery: React.FC = () => {
  const [vehicles, setVehicles] = useState<VehicleInfo[]>([
    { vin: '', plate_no: '' },
  ]);
  const [results, setResults] = useState<VehicleQueryResult[]>([]);
  const [loading, setLoading] = useState(false);
  const [searched, setSearched] = useState(false);

  // 添加车辆输入行
  const addVehicle = () => {
    setVehicles([...vehicles, { vin: '', plate_no: '' }]);
  };

  // 删除车辆输入行
  const removeVehicle = (index: number) => {
    if (vehicles.length <= 1) return;
    setVehicles(vehicles.filter((_, i) => i !== index));
  };

  // 更新车辆信息
  const updateVehicle = (index: number, field: 'vin' | 'plate_no', value: string) => {
    const newVehicles = [...vehicles];
    newVehicles[index] = { ...newVehicles[index], [field]: value };
    setVehicles(newVehicles);
  };

  // 批量查询
  const handleBatchQuery = async () => {
    const validVehicles = vehicles.filter(v => v.vin || v.plate_no);
    if (validVehicles.length === 0) {
      message.warning('请至少输入一个车辆的车架号或车牌号');
      return;
    }

    setLoading(true);
    setSearched(false);

    try {
      // 逐个查询（后端批量接口）
      const allResults: VehicleQueryResult[] = [];

      for (const v of validVehicles) {
        const resp = await queryVehicle(v.vin, v.plate_no);
        allResults.push(resp.data);
      }

      setResults(allResults);
      setSearched(true);

      const foundCount = allResults.filter(r => r.found).length;
      message.success(`查询完成，共找到 ${foundCount}/${allResults.length} 个设备的TID信息`);
    } catch (err: any) {
      message.error('查询失败: ' + (err.response?.data?.error || err.message));
    } finally {
      setLoading(false);
    }
  };

  // 批量粘贴
  const handleBatchPaste = () => {
    // 弹出输入框，让用户批量粘贴
    const modal = document.createElement('div');
    // 简单处理：用textarea
  };

  // 清空结果
  const clearResults = () => {
    setResults([]);
    setSearched(false);
  };

  // 结果表格列
  const resultColumns = [
    {
      title: '序号',
      key: 'index',
      width: 60,
      render: (_: any, __: any, index: number) => index + 1,
    },
    {
      title: '车架号(VIN)',
      dataIndex: 'vin',
      key: 'vin',
      width: 180,
      ellipsis: true,
    },
    {
      title: '车牌号',
      dataIndex: 'plate_no',
      key: 'plate_no',
      width: 130,
    },
    {
      title: 'TID设备号',
      dataIndex: 'tid',
      key: 'tid',
      width: 160,
      render: (tid: string, record: VehicleQueryResult) => (
        record.found ? (
          <Tag color="blue" style={{ fontSize: 13, padding: '2px 8px' }}>
            {tid || '—'}
          </Tag>
        ) : (
          <Tag color="default">未找到</Tag>
        )
      ),
    },
    {
      title: '状态',
      dataIndex: 'found',
      key: 'found',
      width: 100,
      render: (found: boolean) => found ? (
        <Tag icon={<CheckCircleOutlined />} color="success">已找到</Tag>
      ) : (
        <Tag icon={<CloseCircleOutlined />} color="error">未找到</Tag>
      ),
    },
    {
      title: '绑定历史',
      key: 'history',
      width: 100,
      render: (_: any, record: VehicleQueryResult) => (
        record.bind_history && record.bind_history.length > 0 ? (
          <Tooltip
            title={
              <div style={{ maxHeight: 300, overflow: 'auto' }}>
                {record.bind_history.map((h, i) => (
                  <div key={i} style={{ padding: '4px 0', borderBottom: i < record.bind_history.length - 1 ? '1px solid #333' : 'none' }}>
                    <div>TID: {h.tid}</div>
                    <div>绑定: {h.bind_time}</div>
                    {h.unbind_time && <div>解绑: {h.unbind_time}</div>}
                    <Tag color={h.is_current ? 'green' : 'default'}>{h.is_current ? '当前' : '历史'}</Tag>
                  </div>
                ))}
              </div>
            }
          >
            <Button size="small" icon={<HistoryOutlined />}>
              {record.bind_history.length}条
            </Button>
          </Tooltip>
        ) : (
          <Text type="secondary">无</Text>
        )
      ),
    },
    {
      title: '备注',
      dataIndex: 'error',
      key: 'error',
      ellipsis: true,
      render: (err: string) => err ? <Text type="warning">{err}</Text> : '—',
    },
  ];

  // 展开的行 - 显示绑定历史详情
  const expandedRowRender = (record: VehicleQueryResult) => {
    if (!record.bind_history || record.bind_history.length === 0) {
      return <Text type="secondary">无绑定历史记录</Text>;
    }
    return (
      <Timeline
        items={record.bind_history.map((h) => ({
          color: h.is_current ? 'green' : 'gray',
          children: (
            <div>
              <Space>
                <Tag color={h.is_current ? 'green' : 'blue'}>TID: {h.tid}</Tag>
                {h.is_current && <Tag color="green">当前绑定</Tag>}
              </Space>
              <div style={{ marginTop: 4 }}>
                <Text type="secondary">绑定时间: {h.bind_time}</Text>
                {h.unbind_time && (
                  <Text type="secondary" style={{ marginLeft: 16 }}>解绑时间: {h.unbind_time}</Text>
                )}
              </div>
              <div style={{ marginTop: 2 }}>
                <Text type="secondary">车牌号: {h.plate_no || '—'}</Text>
              </div>
            </div>
          ),
        }))}
      />
    );
  };

  return (
    <div className="vehicle-query-panel">
      {/* 批量输入区域 */}
      <Card
        title={
          <Space>
            <SearchOutlined />
            <span>车辆信息输入</span>
          </Space>
        }
        extra={
          <Space>
            <Button icon={<PlusOutlined />} onClick={addVehicle}>
              添加一行
            </Button>
            <Button
              type="primary"
              icon={<SearchOutlined />}
              onClick={handleBatchQuery}
              loading={loading}
            >
              批量查询
            </Button>
          </Space>
        }
        style={{ marginBottom: 16 }}
      >
        <div className="vehicle-input-area">
          <table style={{ width: '100%', borderCollapse: 'collapse' }}>
            <thead>
              <tr>
                <th style={{ width: 50, padding: '8px 4px', textAlign: 'center' }}>#</th>
                <th style={{ padding: '8px 4px', textAlign: 'left' }}>车架号 (VIN)</th>
                <th style={{ padding: '8px 4px', textAlign: 'left' }}>车牌号</th>
                <th style={{ width: 60, padding: '8px 4px', textAlign: 'center' }}>操作</th>
              </tr>
            </thead>
            <tbody>
              {vehicles.map((v, index) => (
                <tr key={index}>
                  <td style={{ padding: '4px', textAlign: 'center', verticalAlign: 'top' }}>
                    <Text type="secondary">{index + 1}</Text>
                  </td>
                  <td style={{ padding: '4px' }}>
                    <Input
                      placeholder="输入17位车架号"
                      value={v.vin}
                      onChange={(e) => updateVehicle(index, 'vin', e.target.value)}
                      allowClear
                      style={{ fontFamily: 'monospace' }}
                    />
                  </td>
                  <td style={{ padding: '4px' }}>
                    <Input
                      placeholder="输入车牌号"
                      value={v.plate_no}
                      onChange={(e) => updateVehicle(index, 'plate_no', e.target.value)}
                      allowClear
                    />
                  </td>
                  <td style={{ padding: '4px', textAlign: 'center' }}>
                    <Button
                      type="text"
                      danger
                      icon={<DeleteOutlined />}
                      onClick={() => removeVehicle(index)}
                      disabled={vehicles.length <= 1}
                    />
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>

        <Alert
          message="使用说明"
          description={'输入车辆的车架号(VIN)或车牌号，点击"批量查询"将从数据库中查询对应的TID设备号和绑定历史。可同时输入多辆车的信息进行批量查询。'}
          type="info"
          showIcon
          style={{ marginTop: 12 }}
        />
      </Card>

      {/* 查询结果 */}
      {loading && (
        <div style={{ textAlign: 'center', padding: 40 }}>
          <Spin size="large" tip="正在查询车辆信息..." />
        </div>
      )}

      {!loading && searched && (
        <Card
          title={
            <Space>
              <CheckCircleOutlined style={{ color: '#52c41a' }} />
              <span>查询结果</span>
            </Space>
          }
          extra={
            <Button icon={<ReloadOutlined />} onClick={clearResults}>
              清空结果
            </Button>
          }
        >
          {results.length > 0 ? (
            <Table
              dataSource={results}
              columns={resultColumns}
              rowKey={(_, index) => String(index)}
              pagination={false}
              size="middle"
              expandable={{
                expandedRowRender,
                rowExpandable: (record: VehicleQueryResult) =>
                  record.bind_history && record.bind_history.length > 0,
              }}
              summary={() => {
                const foundCount = results.filter(r => r.found).length;
                return (
                  <Table.Summary.Row>
                    <Table.Summary.Cell index={0} colSpan={7}>
                      <Text>
                        合计: <Text strong>{results.length}</Text> 辆车，
                        已找到TID: <Text strong style={{ color: '#52c41a' }}>{foundCount}</Text> 辆，
                        未找到: <Text strong style={{ color: '#ff4d4f' }}>{results.length - foundCount}</Text> 辆
                      </Text>
                    </Table.Summary.Cell>
                  </Table.Summary.Row>
                );
              }}
            />
          ) : (
            <Alert message="请输入车辆信息进行查询" type="warning" showIcon />
          )}
        </Card>
      )}
    </div>
  );
};

export default VehicleQuery;
