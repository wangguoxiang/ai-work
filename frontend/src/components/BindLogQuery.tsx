import React, { useState, useCallback, useRef } from 'react';
import {
  Card,
  Input,
  Button,
  Table,
  Tag,
  Space,
  Typography,
  DatePicker,
  message,
  Tooltip,
  Checkbox,
  Row,
  Col,
  Alert,
} from 'antd';
import {
  SearchOutlined,
  DownloadOutlined,
  LinkOutlined,
  MinusCircleOutlined,
} from '@ant-design/icons';
import dayjs from 'dayjs';
import { queryBindLog, BindSegment } from '../api';

const { TextArea } = Input;
const { Text, Title } = Typography;
const { RangePicker } = DatePicker;

const BindLogQuery: React.FC = () => {
  const [vinsText, setVinsText] = useState('GF6ZVRSH57B0XJ210');
  const [dateRange, setDateRange] = useState<[dayjs.Dayjs, dayjs.Dayjs]>([
    dayjs().subtract(1, 'year'),
    dayjs(),
  ]);
  const [loading, setLoading] = useState(false);
  const [data, setData] = useState<BindSegment[]>([]);
  const [total, setTotal] = useState(0);
  const [vins, setVins] = useState<string[]>([]);
  const [searched, setSearched] = useState(false);
  const [wiredOnlyExport, setWiredOnlyExport] = useState(false);

  // Parse VINs from textarea
  const parseVINs = useCallback((raw: string): string[] => {
    return raw
      .split(/[\s,;]+/)
      .map((s) => s.trim())
      .filter(Boolean)
      .filter((v, i, a) => a.indexOf(v) === i);
  }, []);

  // Handle query
  const handleQuery = async () => {
    const raw = vinsText.trim();
    if (!raw) {
      message.warning('请输入 VIN 车架号');
      return;
    }
    if (!dateRange[0] || !dateRange[1]) {
      message.warning('请选择日期范围');
      return;
    }

    const parsedVins = parseVINs(raw);
    if (parsedVins.length === 0) {
      message.warning('未识别到有效的 VIN');
      return;
    }

    setLoading(true);
    setSearched(false);

    try {
      const resp = await queryBindLog({
        vins: parsedVins,
        start: dateRange[0].format('YYYY-MM-DD'),
        end: dateRange[1].format('YYYY-MM-DD'),
      });
      setData(resp.data.results);
      setTotal(resp.data.total);
      setVins(parsedVins);
      setSearched(true);

      if (resp.data.results.length === 0) {
        message.info('该时间窗口内没有处于绑定状态的设备');
      } else {
        message.success(
          `查询完成，共 ${resp.data.total} 条记录 · ${parsedVins.length} 个 VIN`
        );
      }
    } catch (err: any) {
      message.error('查询失败: ' + (err.response?.data?.error || err.message));
      setData([]);
      setTotal(0);
    } finally {
      setLoading(false);
    }
  };

  // Export CSV
  const handleExportCSV = () => {
    if (data.length === 0) return;

    const src = wiredOnlyExport ? data.filter((r) => r.is_wired) : data;
    if (src.length === 0) {
      message.warning('当前结果中没有有线设备，无法导出');
      return;
    }

    const cols: (keyof BindSegment)[] = ['tid', 'sn', 'bind_ts', 'unbind_ts'];
    const header = 'tid,sn,bind_time,unbind_time';
    const csvCell = (v: any): string => {
      if (v == null) v = '';
      v = String(v);
      if (/[",\r\n]/.test(v)) return '"' + v.replace(/"/g, '""') + '"';
      return v;
    };
    const lines = src.map((r) => {
      const cells = cols.map((c) => csvCell(r[c]));
      if (!r.unbind_ts) cells[3] = '';
      return cells.join(',');
    });
    const csv = '\ufeff' + [header].concat(lines).join('\r\n');
    const blob = new Blob([csv], { type: 'text/csv;charset=utf-8;' });
    const a = document.createElement('a');
    a.href = URL.createObjectURL(blob);
    const tag = wiredOnlyExport ? '_wired' : '';
    a.download = `bind_log${tag}_${dayjs().format('YYYYMMDDHHmmss')}.csv`;
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
    URL.revokeObjectURL(a.href);
  };

  // Columns
  const columns = [
    {
      title: '#',
      key: 'index',
      width: 50,
      render: (_: any, __: any, index: number) => index + 1,
    },
    {
      title: 'VIN',
      dataIndex: 'vin',
      key: 'vin',
      width: 170,
      ellipsis: true,
    },
    {
      title: 'TID',
      dataIndex: 'tid',
      key: 'tid',
      width: 180,
      ellipsis: true,
    },
    {
      title: 'SN',
      dataIndex: 'sn',
      key: 'sn',
      width: 130,
      render: (sn: string) => sn || <Text type="secondary">-</Text>,
    },
    {
      title: '设备类型',
      dataIndex: 'sn_type',
      key: 'sn_type',
      width: 110,
      render: (type: string, record: BindSegment) =>
        type ? (
          <Tag color={record.is_wired ? 'green' : 'gold'}>{type}</Tag>
        ) : (
          <Text type="secondary">-</Text>
        ),
    },
    {
      title: 'CNUM',
      dataIndex: 'cnum',
      key: 'cnum',
      width: 100,
      render: (cnum: string) => cnum || <Text type="secondary">-</Text>,
    },
    {
      title: '绑定时间',
      dataIndex: 'bind_time',
      key: 'bind_time',
      width: 170,
    },
    {
      title: '解绑时间',
      key: 'unbind_time',
      width: 170,
      render: (_: any, record: BindSegment) =>
        record.unbind_time ? (
          record.unbind_time
        ) : (
          <Text type="secondary" italic>
            至今未解绑
          </Text>
        ),
    },
    {
      title: '状态',
      key: 'status',
      width: 90,
      render: (_: any, record: BindSegment) =>
        record.unbind_time ? (
          <Tag icon={<MinusCircleOutlined />} color="error">
            已解绑
          </Tag>
        ) : (
          <Tag icon={<LinkOutlined />} color="success">
            绑定中
          </Tag>
        ),
    },
  ];

  return (
    <div>
      {/* 查询区域 */}
      <Card
        title={
          <Space>
            <SearchOutlined />
            <span>设备绑定流水查询 · t_bind_log</span>
          </Space>
        }
        style={{ marginBottom: 16 }}
      >
        <div style={{ marginBottom: 12 }}>
          <Text strong>VIN 车架号</Text>
          <Text type="secondary" style={{ marginLeft: 8, fontSize: 12 }}>
            (支持批量，每行一个，或用逗号/空格/分号分隔)
          </Text>
        </div>
        <TextArea
          rows={4}
          value={vinsText}
          onChange={(e) => setVinsText(e.target.value)}
          placeholder="GF6ZVRSH57B0XJ210&#10;另一VIN..."
          style={{ fontFamily: 'monospace', marginBottom: 12 }}
        />

        <Row gutter={16} align="middle">
          <Col>
            <Text strong>时间窗口：</Text>
          </Col>
          <Col>
            <RangePicker
              value={dateRange}
              onChange={(dates) => {
                if (dates && dates[0] && dates[1]) {
                  setDateRange([dates[0], dates[1]]);
                }
              }}
              allowClear={false}
            />
          </Col>
          <Col>
            <Button
              type="primary"
              icon={<SearchOutlined />}
              onClick={handleQuery}
              loading={loading}
              size="middle"
            >
              查询
            </Button>
          </Col>
        </Row>

        {searched && (
          <Alert
            style={{ marginTop: 12 }}
            message={`共 ${total} 条 · ${vins.length} 个 VIN · 窗口 ${dateRange[0].format('YYYY-MM-DD')} ~ ${dateRange[1].format('YYYY-MM-DD')}`}
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
          searched && data.length > 0 ? (
            <Space>
              <Checkbox
                checked={wiredOnlyExport}
                onChange={(e) => setWiredOnlyExport(e.target.checked)}
              >
                仅导出有线设备
              </Checkbox>
              <Button
                icon={<DownloadOutlined />}
                onClick={handleExportCSV}
              >
                导出 CSV
              </Button>
            </Space>
          ) : undefined
        }
      >
        {!searched ? (
          <div style={{ textAlign: 'center', padding: '40px 0', color: '#999' }}>
            请输入查询条件后点击"查询"
          </div>
        ) : data.length === 0 ? (
          <div style={{ textAlign: 'center', padding: '40px 0', color: '#999' }}>
            该时间窗口内没有处于绑定状态的设备
          </div>
        ) : (
          <Table
            dataSource={data}
            columns={columns}
            rowKey={(r) => `${r.tid}_${r.vin}_${r.bind_ts}`}
            pagination={{
              showSizeChanger: true,
              showQuickJumper: true,
              pageSizeOptions: ['100', '200', '500'],
              defaultPageSize: 100,
              showTotal: (t) => `共 ${t} 条`,
            }}
            scroll={{ x: 1200 }}
            size="small"
          />
        )}
      </Card>

      {/* 说明 */}
      <Card style={{ marginTop: 16 }} size="small">
        <Text type="secondary">
          <strong>说明：</strong>
          <code>t_bind_log</code> 是操作流水表，无独立 bind/unbind 字段。
          后端按 <code>(vin, tid)</code> 分组、按 <code>op_time</code> 排序，在内存里把相邻的"绑定(0) → 解绑(2)"配对成一段；
          若绑定后无解绑，则该段解绑时间为空（至今绑定）。
          支持批量查询多个 VIN（每行一个，或用逗号/空格/分号分隔）。
          若同一设备未解绑又被重新绑定，只算一次，并取较早的绑定时间为准。
        </Text>
      </Card>
    </div>
  );
};

export default BindLogQuery;
