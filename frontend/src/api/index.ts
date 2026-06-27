import axios from 'axios';

const api = axios.create({
  baseURL: '/ehi2/api',
  timeout: 30000,
  headers: {
    'Content-Type': 'application/json',
  },
});

// ============ 类型定义 ============

export interface DBConfig {
  host: string;
  port: number;
  user: string;
  password: string;
  db_name: string;
}

export interface COSConfig {
  secret_id: string;
  secret_key: string;
  bucket: string;
  region: string;
  base_dir: string;
}

export interface AppConfig {
  temp_db: DBConfig;
  vehicle_db: DBConfig;
  cos_config: COSConfig;
  work_dir: string;
  worker_count: number;
  archive_dir?: string;
  archive_file?: string;
  output_dir?: string;
}

export interface VehicleInfo {
  vin: string;
  plate_no: string;
}

export interface BindRecord {
  tid: string;
  vin: string;
  plate_no: string;
  bind_time: string;
  unbind_time: string;
  is_current: boolean;
}

export interface VehicleQueryResult {
  vin: string;
  plate_no: string;
  tid: string;
  found: boolean;
  bind_history: BindRecord[];
  error?: string;
}

export interface BatchQueryResult {
  total: number;
  results: VehicleQueryResult[];
}

export interface TaskStatus {
  task_id: string;
  status: string;
  progress: number;
  stage: string;
  total_files: number;
  processed_files: number;
  total_records: number;
  filtered_records: number;
  exported_records: number;
  current_file: string;
  tids: string[];
  cos_files: string[];
  start_time: string;
  end_time: string;
  error?: string;
  start_at: string;
  elapsed: string;
  logs: string[];
}

export interface ArchiveFileInfo {
  file_name: string;
  file_path: string;
  file_size: number;
}

export interface COSFileInfo {
  key: string;
  name: string;
  size: number;
  size_str: string;
  last_mod: string;
}

export interface FilterStartRequest {
  tids: string[];
  start_time: string;
  end_time: string;
  archive_dir?: string;
  archive_file?: string;
  output_dir?: string;
  worker_count?: number;
}

// ============ API 方法 ============

// 健康检查
export const healthCheck = () => api.get('/health');

// 获取配置
export const getConfig = () => api.get<AppConfig>('/config');

// 更新配置
export const updateConfig = (updates: Record<string, any>) =>
  api.put<AppConfig>('/config', updates);

// 保存完整配置
export const saveFullConfig = (cfg: AppConfig) =>
  api.post<AppConfig>('/config', cfg);

// 查询单个车辆
export const queryVehicle = (vin: string, plate_no: string) =>
  api.post<VehicleQueryResult>('/vehicle/query', { vin, plate_no });

// 批量查询车辆
export const batchQueryVehicle = (vehicles: VehicleInfo[]) =>
  api.post<BatchQueryResult>('/vehicle/batch-query', { vehicles });

// 查询TID历史
export const queryTIDHistory = (tid: string) =>
  api.post('/vehicle/tid-history', { tid });

// 获取归档文件列表
export const listArchiveFiles = () =>
  api.get<{ dir: string; total: number; files: ArchiveFileInfo[] }>('/archive/files');

// 启动过滤任务
export const startFilterTask = (req: FilterStartRequest) =>
  api.post<{ task_id: string; message: string }>('/filter/start', req);

// 获取任务状态
export const getTaskStatus = (taskId: string) =>
  api.get<TaskStatus>(`/filter/task/${taskId}`);

// 获取任务列表
export const listTasks = () =>
  api.get<{ total: number; tasks: TaskStatus[] }>('/filter/tasks');

// 删除任务
export const deleteTask = (taskId: string) =>
  api.delete(`/filter/task/${taskId}`);

// ============ BindLog 设备绑定流水查询 ============

export interface BindLogRequest {
  vins: string[];
  start: string;
  end: string;
}

export interface BindSegment {
  tid: string;
  sn: string;
  vin: string;
  cnum: string;
  bind_time: string;
  unbind_time?: string;
  bind_ts: number;
  unbind_ts?: number;
  sn_type: string;
  is_wired: boolean;
}

export interface BindLogResponse {
  vins: string[];
  start: string;
  end: string;
  total: number;
  results: BindSegment[];
}

// 查询设备绑定流水
export const queryBindLog = (req: BindLogRequest) =>
  api.post<BindLogResponse>('/bindlog/query', req);

// ============ CSV导入TID（按时间范围） ============

export interface TIDImportItem {
  tid: string;
  vin: string;
  plate_no: string;
}

export interface TIDImportResponse {
  total: number;
  tids: TIDImportItem[];
  file_path?: string;
  file_name?: string;
}

// 上传绑定流水CSV文件，返回所有TID列表（含车架号和车牌号）
export const importCSV = (file: File) => {
  const formData = new FormData();
  formData.append('file', file);
  return api.post<TIDImportResponse>('/filter/import-csv', formData, {
    headers: { 'Content-Type': 'multipart/form-data' },
  });
};

// ============ COS存储桶 ============

// 列出COS存储桶中的文件
export const listCOSFiles = (prefix?: string) =>
  api.get<{ total: number; files: COSFileInfo[] }>('/cos/files', {
    params: prefix ? { prefix } : {},
  });

// ============ COS过滤任务 ============

export interface CreateCOSFilterTaskRequest {
  tids: string[];
  vins: string[];
  plate_nos: string[];
  start_time: string;
  end_time: string;
  cos_files: string[];
}

// 创建COS过滤任务
export const createCOSFilterTask = (req: CreateCOSFilterTaskRequest) =>
  api.post<{ task_id: string; message: string }>('/filter/cos-task', req);

// ============ CSV过滤任务(从gzip SQL文件按CSV绑定段过滤) ============

export interface CSVFilterRequest {
  tar_paths?: string[];
  tar_path?: string;
  csv_path: string;
  output_path?: string;
  restart?: boolean;
}

export interface CSVSubmittedTask {
  tar_path: string;
  task_id: string;
  resumed_from: number;
  error?: string;
}

export interface CSVFilterTask {
  id: string;
  tar_path: string;
  csv_path: string;
  output_path: string;
  status: string;
  error?: string;
  started_at: number;
  updated_at: number;
  finished_at?: number;
  lines_done: number;
  raw_lines: number;
  kept_lines: number;
  first_ts: number;
  last_ts: number;
  resumed: boolean;
  pct: number;
  submit_order: number;
}

// 提交CSV过滤任务
export const submitCSVFilter = (req: CSVFilterRequest) =>
  api.post<{ tasks: CSVSubmittedTask[] }>('/filter/csv-submit', req);

// 获取CSV过滤任务列表
export const listCSVFilterTasks = () =>
  api.get<{ tasks: CSVFilterTask[] }>('/filter/csv-tasks');

// 取消CSV过滤任务
export const cancelCSVFilterTask = (id: string) =>
  api.get('/filter/csv-cancel', { params: { id } });

// 上传CSV文件到服务器
export const uploadCSVFile = (file: File) => {
  const formData = new FormData();
  formData.append('file', file);
  return api.post<{ file_name: string; file_path: string; file_size: number }>('/filter/csv-upload', formData, {
    headers: { 'Content-Type': 'multipart/form-data' },
    timeout: 60000,
  });
};

// COS文件下载(异步，返回task_id用于轮询进度)
// force=true 时强制覆盖已存在的本地文件
export const downloadCOSFile = (cosKey: string, force?: boolean) =>
  api.post<{ task_id: string; cos_key: string; local_path: string; file_name: string }>('/cos/download', {
    cos_key: cosKey,
    force: force || false,
  }, {
    timeout: 30000,
  });

// 查询下载进度(前端每5秒轮询)
export const getDownloadProgress = (taskId: string) =>
  api.get<{ task_id: string; progress: number; message: string; local_path: string; file_name: string; error: string; done: boolean }>('/cos/download-progress', {
    params: { task_id: taskId },
  });

// COS下载任务进度条目
export interface DownloadProgressItem {
  task_id: string;
  progress: number;
  message: string;
  local_path: string;
  file_name: string;
  error?: string;
  done: boolean;
}

// 列出所有COS下载任务进度（刷新页面后恢复用）
export const listDownloads = () =>
  api.get<{ total: number; tasks: DownloadProgressItem[] }>('/cos/downloads');

export default api;
