import React, { useState } from 'react';
import {
  Card,
  Input,
  Button,
  Table,
  Tag,
  Space,
  Typography,
  Row,
  Col,
  InputNumber,
  message,
  Alert,
} from 'antd';
import {
  SearchOutlined,
  DownloadOutlined,
  DatabaseOutlined,
} from '@ant-design/icons';
import dayjs from 'dayjs';
import { queryKongCheDevices, KongCheDevice } from '../api';

const { Text } = Typography;

const KongCheQuery: React.FC = () => {
  const [sn, setSn] = useState('');
  const [deviceId, setDeviceId] = useState('');
  const [limit, setLimit] = useState(500);
  const [loading, setLoading] = useState(false);
  const [devices, setDevices] = useState<KongCheDevice[]>([]);
  const [total, setTotal] = useState(0);
  const [searched, setSearched] = useState(false);

  const handleQuery = async () => {
    const snTrim = sn.trim();
    const idTrim = deviceId.trim();
    if (!snTrim && !idTrim) {
      message.warning('请至少输入 SN 或 device id 作为查询条件');
      return;
    }

    setLoading(true);
    setSearched(false);
    try {
      const resp = await queryKongCheDevices({
        sn: snTrim || undefined,
        device_id: idTrim || undefined,
        limit,
      });
      setDevices(resp.data.devices || []);
      setTotal(resp.data.total);
      setSearched(true);

      if ((resp.data.devices || []).length === 0) {
        message.info('未查询到匹配的设备');
      } else {
        message.success(`查询完成，共 ${resp.data.total} 条记录`);
      }
    } catch (err: any) {
      message.error('查询失败: ' + (err.response?.data?.error || err.message));
      setDevices([]);
      setTotal(0);
    } finally {
      setLoading(false);
    }
  };

  // 导出 CSV (device_id,sn 表头, 便于直接用于控车过滤)
  const handleExportCSV = () => {
    if (devices.length === 0) return;

    const csvCell = (v: any): string => {
      if (v == null) v = '';
      v = String(v);
      if (/[",\r\n]/.test(v)) return '"' + v.replace(/"/g, '""') + '"';
      return v;
    };
    const header = 'device_id,sn';
    const lines = devices.map((d) =>
      [csvCell(d.device_id), csvCell(d.sn)].join(',')
    );
    const csv = '\ufeff' + [header].concat(lines).join('\r\n');
    const blob = new Blob([csv], { type: 'text/csv;charset=utf-8;' });
    const a = document.createElement('a');
    a.href = URL.createObjectURL(blob);
    a.download = `kongche_device_${dayjs().format('YYYYMMDDHHmmss')}.csv`;
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
    URL.revokeObjectURL(a.href);
  };

  const columns = [
    {
      title: '#',
      key: 'index',
      width: 50,
      render: (_: any, __: any, index: number) => index + 1,
    },
    {
      title: 'Device ID',
      dataIndex: 'device_id',
      key: 'device_id',
      width: 220,
      render: (v: string) => (
        <Text style={{ fontFamily: 'monospace' }}>{v}</Text>
      ),
    },
    {
      title: 'SN 序列号',
      dataIndex: 'sn',
      key: 'sn',
      width: 200,
      render: (v: string) => (
        <Text style={{ fontFamily: 'monospace' }}>{v || '-'}</Text>
      ),
    },
  ];

  return (
    <div>
      {/* 查询区域 */}
      <Card
        title={
          <Space>
            <DatabaseOutlined />
            <span>控车系统设备查询 · SN / device id</span>
          </Space>
        }
        style={{ marginBottom: 16 }}
      >
        <Row gutter={16} align="bottom">
          <Col xs={24} sm={8}>
            <div style={{ marginBottom: 6 }}>
              <Text strong>SN 序列号</Text>
            </div>
            <Input
              value={sn}
              onChange={(e) => setSn(e.target.value)}
              placeholder="支持模糊匹配, 如: 1111F64888"
              allowClear
              onPressEnter={handleQuery}
            />
          </Col>
          <Col xs={24} sm={8}>
            <div style={{ marginBottom: 6 }}>
              <Text strong>Device ID</Text>
            </div>
            <Input
              value={deviceId}
              onChange={(e) => setDeviceId(e.target.value)}
              placeholder="支持模糊匹配"
              allowClear
              onPressEnter={handleQuery}
            />
          </Col>
          <Col xs={12} sm={4}>
            <div style={{ marginBottom: 6 }}>
              <Text strong>返回上限</Text>
            </div>
            <InputNumber
              value={limit}
              onChange={(v) => setLimit(v || 500)}
              min={1}
              max={5000}
              style={{ width: '100%' }}
            />
          </Col>
          <Col xs={12} sm={4}>
            <Button
              type="primary"
              icon={<SearchOutlined />}
              onClick={handleQuery}
              loading={loading}
              block
            >
              查询
            </Button>
          </Col>
        </Row>
        <Text type="secondary" style={{ fontSize: 12 }}>
          SN 和 device id 至少填写一项; 两者都填时按"与"关系过滤。
        </Text>

        {searched && (
          <Alert
            style={{ marginTop: 12 }}
            message={`共 ${total} 条设备记录 · 条件: SN="${sn}" device_id="${deviceId}"`}
            type="info"
            showIcon
          />
        )}
      </Card>

      {/* 结果区域 */}
      <Card
        title={
          <Space>
            <span>查询结果</span>
            {searched && <Tag color="blue">{total} 条</Tag>}
          </Space>
        }
        extra={
          searched && devices.length > 0 ? (
            <Button icon={<DownloadOutlined />} onClick={handleExportCSV}>
              导出 CSV
            </Button>
          ) : undefined
        }
      >
        {!searched ? (
          <div style={{ textAlign: 'center', padding: '40px 0', color: '#999' }}>
            请输入查询条件后点击"查询"
          </div>
        ) : devices.length === 0 ? (
          <div style={{ textAlign: 'center', padding: '40px 0', color: '#999' }}>
            未查询到匹配的设备
          </div>
        ) : (
          <Table
            dataSource={devices}
            columns={columns}
            rowKey={(r) => `${r.device_id}_${r.sn}`}
            pagination={{
              showSizeChanger: true,
              showQuickJumper: true,
              pageSizeOptions: ['100', '200', '500'],
              defaultPageSize: 100,
              showTotal: (t) => `共 ${t} 条`,
            }}
            scroll={{ x: 600 }}
            size="small"
          />
        )}
      </Card>

      {/* 说明 */}
      <Card style={{ marginTop: 16 }} size="small">
        <Text type="secondary">
          <strong>说明：</strong>
          数据来源为控车数据库 <code>positioning_travel.device</code> 表。
          导出的 CSV 表头为 <code>device_id,sn</code>，可直接在"控车过滤"页面导入，
          用于从 COS 压缩数据文件中按 device id 过滤数据（过滤列: device_id=第1列, create_time=第3列）。
        </Text>
      </Card>
    </div>
  );
};

export default KongCheQuery;
