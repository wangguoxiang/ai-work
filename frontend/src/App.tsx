import React, { useState } from 'react';
import {
  Layout,
  Tabs,
  Typography,
  Space,
} from 'antd';
import {
  ApiOutlined,
  LinkOutlined,
} from '@ant-design/icons';
import VehicleQuery from './components/VehicleQuery';
import ConfigPanel from './components/ConfigPanel';
import FilterTask from './components/FilterTask';
import TaskList from './components/TaskList';
import BindLogQuery from './components/BindLogQuery';

const { Header, Content } = Layout;
const { Title } = Typography;

const App: React.FC = () => {
  const [activeTab, setActiveTab] = useState('vehicle');

  const handleConfigSaved = () => {
    // 配置保存后可以做一些全局处理
  };

  const items = [
    {
      key: 'vehicle',
      label: '🔍 车辆查询',
      children: <VehicleQuery />,
    },
    {
      key: 'bindlog',
      label: <><LinkOutlined /> 绑定流水</>,
      children: <BindLogQuery />,
    },
    {
      key: 'filter',
      label: '⚙️ 过滤任务',
      children: <FilterTask />,
    },
    {
      key: 'tasks',
      label: '📋 任务列表',
      children: <TaskList />,
    },
    {
      key: 'config',
      label: '🛠️ 系统配置',
      children: <ConfigPanel onSaved={handleConfigSaved} />,
    },
  ];

  return (
    <Layout className="app-container">
      <Header className="app-header">
        <ApiOutlined className="header-icon" />
        <Title level={4} style={{ color: '#fff', margin: '0 0 0 12px' }}>
          GPS归档数据过滤工具 · 设备绑定流水查询
        </Title>
      </Header>
      <Content className="app-content">
        <Tabs
          activeKey={activeTab}
          onChange={setActiveTab}
          items={items}
          size="large"
        />
      </Content>
    </Layout>
  );
};

export default App;
