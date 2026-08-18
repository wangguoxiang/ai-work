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
  FilterOutlined,
  EnvironmentOutlined,
  CarOutlined,
} from '@ant-design/icons';
import VehicleQuery from './components/VehicleQuery';
import ConfigPanel from './components/ConfigPanel';
import FilterTask from './components/FilterTask';
import TaskList from './components/TaskList';
import BindLogQuery from './components/BindLogQuery';
import CSVFilter from './components/CSVFilter';
import ReverseGeo from './components/ReverseGeo';
import KongCheQuery from './components/KongCheQuery';
import KongCheFilter from './components/KongCheFilter';

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
      key: 'csv-filter',
      label: <><FilterOutlined /> CSV过滤</>,
      children: <CSVFilter />,
    },
    {
      key: 'tasks',
      label: '📋 任务列表',
      children: <TaskList />,
    },
    {
      key: 'reversegeo',
      label: <><EnvironmentOutlined /> 逆地址转换</>,
      children: <ReverseGeo />,
    },
    {
      key: 'kongche-query',
      label: <><CarOutlined /> 控车设备</>,
      children: <KongCheQuery />,
    },
    {
      key: 'kongche-filter',
      label: <><FilterOutlined /> 控车过滤</>,
      children: <KongCheFilter />,
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
          GPS归档数据过滤工具 · 逆地址转换
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
