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
} from 'antd';
import {
  SaveOutlined,
  DatabaseOutlined,
  FolderOpenOutlined,
  SettingOutlined,
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
      };

      await saveFullConfig(cfgToSave);
      message.success('配置保存成功');
      setTempPassword('');
      setVehiclePassword('');
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
