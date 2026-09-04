import React, { useState, useEffect } from 'react';
import {
  Card,
  Form,
  Input,
  InputNumber,
  Button,
  Space,
  Divider,
  message,
  Spin,
  Alert,
  Typography,
  Row,
  Col,
  Radio,
} from 'antd';
import {
  SaveOutlined,
  DatabaseOutlined,
  FolderOpenOutlined,
  SettingOutlined,
  CloudServerOutlined,
} from '@ant-design/icons';
import { getConfig, saveFullConfig, AppConfig } from '../api';

const { Text } = Typography;

interface Props {
  onSaved?: () => void;
}

const ConfigPanel: React.FC<Props> = ({ onSaved }) => {
  const [config, setConfig] = useState<AppConfig | null>(null);
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [tempPassword, setTempPassword] = useState('');
  const [vehiclePassword, setVehiclePassword] = useState('');
  const [kongchePassword, setKongchePassword] = useState('');
  const [cosSecretKey, setCosSecretKey] = useState('');

  // 加载配置
  useEffect(() => {
    loadConfig();
  }, []);

  const loadConfig = async () => {
    setLoading(true);
    try {
      const resp = await getConfig();
      setConfig(resp.data);
    } catch (err: any) {
      message.error('加载配置失败: ' + (err.response?.data?.error || err.message));
    } finally {
      setLoading(false);
    }
  };

  // 保存配置
  const handleSave = async () => {
    if (!config) return;
    setSaving(true);

    try {
      const cfgToSave = {
        ...config,
        temp_db: {
          ...config.temp_db,
          password: tempPassword || config.temp_db.password,
        },
        vehicle_db: {
          ...config.vehicle_db,
          password: vehiclePassword || config.vehicle_db.password,
        },
        kongche_db: {
          ...config.kongche_db,
          password: kongchePassword || config.kongche_db?.password || '',
        },
        cos_config: {
          ...config.cos_config,
          secret_key: cosSecretKey || config.cos_config?.secret_key || '',
        },
      };

      await saveFullConfig(cfgToSave);
      message.success('配置保存成功');
      setTempPassword('');
      setVehiclePassword('');
      setKongchePassword('');
      setCosSecretKey('');
      onSaved?.();
    } catch (err: any) {
      message.error('保存配置失败: ' + (err.response?.data?.error || err.message));
    } finally {
      setSaving(false);
    }
  };

  if (loading) {
    return (
      <div className="loading-overlay">
        <Spin size="large" tip="加载配置中..." />
      </div>
    );
  }

  if (!config) {
    return <Alert message="无法加载配置" type="error" showIcon />;
  }

  return (
    <div className="config-panel">
      <Row gutter={24}>
        {/* 临时数据库配置 */}
        <Col xs={24} lg={12}>
          <Card
            title={
              <Space>
                <DatabaseOutlined />
                <span>临时数据库 (用于存放过滤后的数据)</span>
              </Space>
            }
            style={{ marginBottom: 16 }}
          >
            <Form layout="vertical">
              <Form.Item label="主机地址">
                <Input
                  value={config.temp_db.host}
                  onChange={(e) =>
                    setConfig({
                      ...config,
                      temp_db: { ...config.temp_db, host: e.target.value },
                    })
                  }
                  placeholder="127.0.0.1"
                />
              </Form.Item>
              <Form.Item label="端口">
                <InputNumber
                  value={config.temp_db.port}
                  onChange={(v) =>
                    setConfig({
                      ...config,
                      temp_db: { ...config.temp_db, port: v || 3306 },
                    })
                  }
                  min={1}
                  max={65535}
                  style={{ width: '100%' }}
                />
              </Form.Item>
              <Form.Item label="用户名">
                <Input
                  value={config.temp_db.user}
                  onChange={(e) =>
                    setConfig({
                      ...config,
                      temp_db: { ...config.temp_db, user: e.target.value },
                    })
                  }
                  placeholder="root"
                />
              </Form.Item>
              <Form.Item label="密码">
                <Input.Password
                  value={tempPassword}
                  onChange={(e) => setTempPassword(e.target.value)}
                  placeholder="输入密码（留空则不修改）"
                />
              </Form.Item>
              <Form.Item label="数据库名">
                <Input
                  value={config.temp_db.db_name}
                  onChange={(e) =>
                    setConfig({
                      ...config,
                      temp_db: { ...config.temp_db, db_name: e.target.value },
                    })
                  }
                  placeholder="gps_temp"
                />
              </Form.Item>
            </Form>
          </Card>
        </Col>

        {/* 车辆数据库配置 */}
        <Col xs={24} lg={12}>
          <Card
            title={
              <Space>
                <DatabaseOutlined />
                <span>车辆数据库 (用于查询VIN对应的TID)</span>
              </Space>
            }
            style={{ marginBottom: 16 }}
          >
            <Form layout="vertical">
              <Form.Item label="主机地址">
                <Input
                  value={config.vehicle_db.host}
                  onChange={(e) =>
                    setConfig({
                      ...config,
                      vehicle_db: { ...config.vehicle_db, host: e.target.value },
                    })
                  }
                  placeholder="127.0.0.1"
                />
              </Form.Item>
              <Form.Item label="端口">
                <InputNumber
                  value={config.vehicle_db.port}
                  onChange={(v) =>
                    setConfig({
                      ...config,
                      vehicle_db: { ...config.vehicle_db, port: v || 3306 },
                    })
                  }
                  min={1}
                  max={65535}
                  style={{ width: '100%' }}
                />
              </Form.Item>
              <Form.Item label="用户名">
                <Input
                  value={config.vehicle_db.user}
                  onChange={(e) =>
                    setConfig({
                      ...config,
                      vehicle_db: { ...config.vehicle_db, user: e.target.value },
                    })
                  }
                  placeholder="root"
                />
              </Form.Item>
              <Form.Item label="密码">
                <Input.Password
                  value={vehiclePassword}
                  onChange={(e) => setVehiclePassword(e.target.value)}
                  placeholder="输入密码（留空则不修改）"
                />
              </Form.Item>
              <Form.Item label="数据库名">
                <Input
                  value={config.vehicle_db.db_name}
                  onChange={(e) =>
                    setConfig({
                      ...config,
                      vehicle_db: { ...config.vehicle_db, db_name: e.target.value },
                    })
                  }
                  placeholder="vehicle_db"
                />
              </Form.Item>
            </Form>
          </Card>
        </Col>
      </Row>

      <Row gutter={24}>
        {/* 文件路径配置 */}
        <Col xs={24} lg={12}>
          <Card
            title={
              <Space>
                <FolderOpenOutlined />
                <span>文件路径</span>
              </Space>
            }
            style={{ marginBottom: 16 }}
          >
            <Form layout="vertical">
              <Form.Item label="归档数据文件目录" help="MySQL归档数据文件所在目录">
                <Input
                  value={config.archive_dir}
                  onChange={(e) =>
                    setConfig({ ...config, archive_dir: e.target.value })
                  }
                  placeholder="./archive"
                />
              </Form.Item>
              <Form.Item label="归档文件名(可选)" help="指定单个文件，留空则处理目录下所有.sql/.csv文件">
                <Input
                  value={config.archive_file}
                  onChange={(e) =>
                    setConfig({ ...config, archive_file: e.target.value })
                  }
                  placeholder="留空则处理所有文件"
                />
              </Form.Item>
              <Form.Item label="输出目录" help="过滤后的SQL文件输出目录">
                <Input
                  value={config.output_dir}
                  onChange={(e) =>
                    setConfig({ ...config, output_dir: e.target.value })
                  }
                  placeholder="./output"
                />
              </Form.Item>
            </Form>
          </Card>
        </Col>

        {/* 处理参数配置 */}
        <Col xs={24} lg={12}>
          <Card
            title={
              <Space>
                <SettingOutlined />
                <span>处理参数</span>
              </Space>
            }
            style={{ marginBottom: 16 }}
          >
            <Form layout="vertical">
              <Form.Item
                label="并行处理线程数"
                help="同时处理归档文件的worker数量，根据CPU核心数调整"
              >
                <InputNumber
                  value={config.worker_count}
                  onChange={(v) =>
                    setConfig({ ...config, worker_count: v || 4 })
                  }
                  min={1}
                  max={64}
                  style={{ width: '100%' }}
                />
              </Form.Item>
            </Form>
          </Card>
        </Col>
      </Row>

      <Row gutter={24}>
        {/* 控车数据库配置 */}
        <Col xs={24} lg={12}>
          <Card
            title={
              <Space>
                <DatabaseOutlined />
                <span>控车数据库 (设备SN / device id 查询)</span>
              </Space>
            }
            style={{ marginBottom: 16 }}
          >
            <Form layout="vertical">
              <Form.Item label="主机地址">
                <Input
                  value={config.kongche_db?.host}
                  onChange={(e) =>
                    setConfig({
                      ...config,
                      kongche_db: { ...config.kongche_db, host: e.target.value },
                    })
                  }
                  placeholder="127.0.0.1"
                />
              </Form.Item>
              <Form.Item label="端口">
                <InputNumber
                  value={config.kongche_db?.port}
                  onChange={(v) =>
                    setConfig({
                      ...config,
                      kongche_db: { ...config.kongche_db, port: v || 3306 },
                    })
                  }
                  min={1}
                  max={65535}
                  style={{ width: '100%' }}
                />
              </Form.Item>
              <Form.Item label="用户名">
                <Input
                  value={config.kongche_db?.user}
                  onChange={(e) =>
                    setConfig({
                      ...config,
                      kongche_db: { ...config.kongche_db, user: e.target.value },
                    })
                  }
                  placeholder="root"
                />
              </Form.Item>
              <Form.Item label="密码">
                <Input.Password
                  value={kongchePassword}
                  onChange={(e) => setKongchePassword(e.target.value)}
                  placeholder="输入密码（留空则不修改）"
                />
              </Form.Item>
              <Form.Item label="数据库名">
                <Input
                  value={config.kongche_db?.db_name}
                  onChange={(e) =>
                    setConfig({
                      ...config,
                      kongche_db: { ...config.kongche_db, db_name: e.target.value },
                    })
                  }
                  placeholder="positioning_travel"
                />
              </Form.Item>
              <Form.Item label="设备表名">
                <Input
                  value={config.kongche_db?.device_table}
                  onChange={(e) =>
                    setConfig({
                      ...config,
                      kongche_db: { ...config.kongche_db, device_table: e.target.value },
                    })
                  }
                  placeholder="device"
                />
              </Form.Item>
              <Form.Item label="SN 列名">
                <Input
                  value={config.kongche_db?.sn_col}
                  onChange={(e) =>
                    setConfig({
                      ...config,
                      kongche_db: { ...config.kongche_db, sn_col: e.target.value },
                    })
                  }
                  placeholder="sn"
                />
              </Form.Item>
              <Form.Item label="Device ID 列名">
                <Input
                  value={config.kongche_db?.device_id_col}
                  onChange={(e) =>
                    setConfig({
                      ...config,
                      kongche_db: { ...config.kongche_db, device_id_col: e.target.value },
                    })
                  }
                  placeholder="id"
                />
              </Form.Item>
              <Form.Item
                label="COS 目录前缀 (base_dir)"
                help="控车数据在 COS 存储桶中的目录前缀，与 GPS 的 cos_config.base_dir 不同"
              >
                <Input
                  value={config.kongche_db?.cos_base_dir}
                  onChange={(e) =>
                    setConfig({
                      ...config,
                      kongche_db: { ...config.kongche_db, cos_base_dir: e.target.value },
                    })
                  }
                  placeholder="positioning_travel"
                />
              </Form.Item>
            </Form>
          </Card>
        </Col>

        {/* 控车过滤列配置 */}
        <Col xs={24} lg={12}>
          <Card
            title={
              <Space>
                <DatabaseOutlined />
                <span>控车压缩数据过滤列</span>
              </Space>
            }
            style={{ marginBottom: 16 }}
          >
            <Form layout="vertical">
              <Form.Item
                label="device_id 列索引 (0-based)"
                help="mqtt_carstatus_position_log 表中 device_id 在 INSERT VALUES 中的位置，默认 1"
              >
                <InputNumber
                  value={config.kongche_db?.device_id_col_index}
                  onChange={(v) =>
                    setConfig({
                      ...config,
                      kongche_db: { ...config.kongche_db, device_id_col_index: v || 1 },
                    })
                  }
                  min={0}
                  max={100}
                  style={{ width: '100%' }}
                />
              </Form.Item>
              <Form.Item
                label="create_time 列索引 (0-based)"
                help="时间戳列在 INSERT VALUES 中的位置，默认 3"
              >
                <InputNumber
                  value={config.kongche_db?.timestamp_col_index}
                  onChange={(v) =>
                    setConfig({
                      ...config,
                      kongche_db: { ...config.kongche_db, timestamp_col_index: v || 3 },
                    })
                  }
                  min={0}
                  max={100}
                  style={{ width: '100%' }}
                />
              </Form.Item>
              <Form.Item label="连接超时">
                <Input
                  value={config.kongche_db?.timeout}
                  onChange={(e) =>
                    setConfig({
                      ...config,
                      kongche_db: { ...config.kongche_db, timeout: e.target.value },
                    })
                  }
                  placeholder="10s"
                />
              </Form.Item>
              <Alert
                type="info"
                showIcon
                message="说明"
                description={
                  <Text style={{ fontSize: 12 }}>
                    控车设备查询数据源: <code>{config.kongche_db?.db_name || 'positioning_travel'}.{config.kongche_db?.device_table || 'device'}</code> 表。
                    过滤时按 <code>device_id</code>(第 {config.kongche_db?.device_id_col_index || 1} 列) 匹配，
                    时间戳 <code>create_time</code>(第 {config.kongche_db?.timestamp_col_index || 3} 列) 为 Unix 秒。
                  </Text>
                }
              />
            </Form>
          </Card>
        </Col>
      </Row>

      <Row gutter={24}>
        {/* 对象存储桶配置 (腾讯云 COS / 阿里云 OSS) */}
        <Col xs={24} lg={12}>
          <Card
            title={
              <Space>
                <CloudServerOutlined />
                <span>对象存储桶 (腾讯云 COS / 阿里云 OSS)</span>
              </Space>
            }
            style={{ marginBottom: 16 }}
          >
            <Form layout="vertical">
              <Form.Item label="存储提供商" help="选择 GPS 归档文件所在的对象存储服务">
                <Radio.Group
                  value={config.cos_config?.provider || 'tencent'}
                  onChange={(e) =>
                    setConfig({
                      ...config,
                      cos_config: { ...config.cos_config, provider: e.target.value },
                    })
                  }
                >
                  <Radio.Button value="tencent">腾讯云 COS</Radio.Button>
                  <Radio.Button value="aliyun">阿里云 OSS</Radio.Button>
                </Radio.Group>
              </Form.Item>
              <Form.Item
                label={config.cos_config?.provider === 'aliyun' ? 'AccessKey ID' : 'Secret ID'}
              >
                <Input
                  value={config.cos_config?.secret_id}
                  onChange={(e) =>
                    setConfig({
                      ...config,
                      cos_config: { ...config.cos_config, secret_id: e.target.value },
                    })
                  }
                  placeholder={config.cos_config?.provider === 'aliyun' ? 'LTAI...' : 'AKID...'}
                />
              </Form.Item>
              <Form.Item
                label={config.cos_config?.provider === 'aliyun' ? 'AccessKey Secret' : 'Secret Key'}
              >
                <Input.Password
                  value={cosSecretKey}
                  onChange={(e) => setCosSecretKey(e.target.value)}
                  placeholder="输入密钥（留空则不修改）"
                />
              </Form.Item>
              <Form.Item label="存储桶名称 (Bucket)">
                <Input
                  value={config.cos_config?.bucket}
                  onChange={(e) =>
                    setConfig({
                      ...config,
                      cos_config: { ...config.cos_config, bucket: e.target.value },
                    })
                  }
                  placeholder={config.cos_config?.provider === 'aliyun' ? 'my-bucket' : 'my-bucket-1250000000'}
                />
              </Form.Item>
              <Form.Item
                label="地域 (Region)"
                help={
                  config.cos_config?.provider === 'aliyun'
                    ? '阿里云地域,例: cn-hangzhou / cn-shanghai'
                    : '腾讯云地域,例: ap-shanghai / ap-guangzhou'
                }
              >
                <Input
                  value={config.cos_config?.region}
                  onChange={(e) =>
                    setConfig({
                      ...config,
                      cos_config: { ...config.cos_config, region: e.target.value },
                    })
                  }
                  placeholder={config.cos_config?.provider === 'aliyun' ? 'cn-hangzhou' : 'ap-shanghai'}
                />
              </Form.Item>
              {config.cos_config?.provider === 'aliyun' && (
                <Form.Item
                  label="自定义 Endpoint (可选)"
                  help="留空时默认使用内网地址 oss-{region}-internal.aliyuncs.com。如需外网或自定义域名请手动填写"
                >
                  <Input
                    value={config.cos_config?.endpoint}
                    onChange={(e) =>
                      setConfig({
                        ...config,
                        cos_config: { ...config.cos_config, endpoint: e.target.value },
                      })
                    }
                    placeholder="例: oss-cn-hangzhou.aliyuncs.com"
                  />
                </Form.Item>
              )}
              <Form.Item label="目录前缀 (Base Dir)" help="存储桶中的基础目录,例: qjcg/">
                <Input
                  value={config.cos_config?.base_dir}
                  onChange={(e) =>
                    setConfig({
                      ...config,
                      cos_config: { ...config.cos_config, base_dir: e.target.value },
                    })
                  }
                  placeholder="qjcg/"
                />
              </Form.Item>
            </Form>
          </Card>
        </Col>

        {/* 对象存储说明 */}
        <Col xs={24} lg={12}>
          <Card
            title={
              <Space>
                <SettingOutlined />
                <span>存储提供商说明</span>
              </Space>
            }
            style={{ marginBottom: 16 }}
          >
            <Alert
              type="info"
              showIcon
              message="腾讯云 COS"
              description={
                <Text style={{ fontSize: 12 }}>
                  使用 <code>region</code> 自动构造内网地址:
                  <br />
                  <code>https://&#123;bucket&#125;.cos-internal.&#123;region&#125;.myqcloud.com</code>
                  <br />
                  适用于部署在腾讯云 VPC 内的服务器，无需填写 endpoint。
                </Text>
              }
              style={{ marginBottom: 12 }}
            />
            <Alert
              type="info"
              showIcon
              message="阿里云 OSS"
              description={
                <Text style={{ fontSize: 12 }}>
                  默认使用 <code>region</code> 构造内网地址:
                  <br />
                  <code>oss-&#123;region&#125;-internal.aliyuncs.com</code>
                  <br />
                  如需外网访问(例: <code>oss-cn-hangzhou.aliyuncs.com</code>) 或自定义域名，请在
                  “自定义 Endpoint” 中填写完整地址。
                </Text>
              }
              style={{ marginBottom: 12 }}
            />
            <Divider style={{ margin: '12px 0' }} />
            <Text style={{ fontSize: 12 }} type="secondary">
              提示: 切换提供商后请确保 secret_id / secret_key / bucket / region 与目标服务匹配。
              修改保存后后端会自动重建存储客户端。
            </Text>
          </Card>
        </Col>
      </Row>

      <div style={{ textAlign: 'right', marginTop: 16 }}>
        <Space>
          <Button onClick={loadConfig}>重置</Button>
          <Button
            type="primary"
            icon={<SaveOutlined />}
            onClick={handleSave}
            loading={saving}
            size="large"
          >
            保存配置
          </Button>
        </Space>
      </div>
    </div>
  );
};

export default ConfigPanel;
