export type Kind = "php" | "mysql" | "nginx" | "redis";
export interface Package {
  id: string;
  kind: Kind;
  version: string;
  arch: string;
  url: string;
  sha256: string;
  signatureUrl: string;
  signingKeyUrl: string;
  signingFingerprint: string;
  source: string;
  sourceUrl: string;
  channel: string;
  eol: boolean;
  verified: boolean;
  path: string;
  installedAt: string;
}
export interface Instance {
  id: string;
  name: string;
  kind: Kind;
  packageId: string;
  port: number;
  configPath: string;
  dataDir: string;
  logPath: string;
  pid: number;
  processCreated: number;
  executable: string;
  state: string;
  error: string;
  createdAt: string;
}
export interface InstanceInput {
  name: string;
  packageId: string;
  port: number;
  password: string;
}
export interface ManagedDatabase {
  id: string;
  instanceId: string;
  name: string;
  user: string;
  host: string;
  password?: string;
  state: string;
  error: string;
  createdAt: string;
}
export interface DatabaseInfo {
  name: string;
  charset: string;
  collation: string;
  tables: number;
  sizeBytes: number;
  system: boolean;
}
export interface DatabaseTool {
  id: string;
  name: string;
  kind: string;
  path: string;
  url: string;
  source: string;
  custom: boolean;
  canOpen: boolean;
  note: string;
}
export interface DatabaseAccount {
  user: string;
  host: string;
  protected: boolean;
}
export interface DatabaseAccountInput {
  user: string;
  host: string;
  password: string;
}
export interface DatabasePrivilegeInput {
  user: string;
  host: string;
  database: string;
  privileges: string[];
}
export interface Site {
  id: string;
  name: string;
  domain: string;
  secondDomain?: string;
  root: string;
  entry: string;
  port: number;
  https: boolean;
  phpInstanceId: string;
  phpPathInfo?: boolean;
  enabled: boolean;
  rewrite: string;
  createdAt: string;
  lastActiveAt: string;
  updatedAt: string;
  projectFolder?: string;
}
export type SiteSortableKey = "name" | "createdAt" | "lastActiveAt";
export type SiteSortKey = SiteSortableKey | "manual" | "domain" | "root" | "environment" | "enabled";
export interface SiteViewPreferences {
  hiddenColumns: string[];
  siteOrder: string[];
  sortBy: SiteSortKey;
  sortDirection: "asc" | "desc";
}
export interface SiteEntrySelection {
  entry: string;
  selected: boolean;
}
export interface RewritePreset {
  id: string;
  name: string;
  description: string;
  rules: string;
  suggestedEntry: string;
  sourceUrl: string;
}
export interface Task {
  id: string;
  action: string;
  targetId: string;
  state: string;
  progress: number;
  bytesTotal: number;
  bytesDone: number;
  message: string;
  error: string;
  startedAt: string;
}
export interface Settings {
  root: string;
  suggestedRoot: string;
  version: string;
}
export interface UpdateInfo {
  currentVersion: string;
  latestVersion: string;
  available: boolean;
  mandatory: boolean;
  releaseDate: string;
  notes: string;
  sha256: string;
  size: number;
  checkedAt: string;
  configured: boolean;
  error: string;
}
export interface AutoStartStatus {
  enabled: boolean;
  conflict: string;
}
export interface SidebarItem {
  key: Page;
  hidden: boolean;
  label: string;
}
export interface Diagnostic {
  level: string;
  message: string;
}
export interface RuntimeDependency {
  id: string;
  name: string;
  state: "ready" | "missing" | "error";
  version: string;
  message: string;
  requiredBy: string[];
  guide: string;
}
export interface DependencyReport {
  checkedAt: string;
  items: RuntimeDependency[];
  warnings: string[];
}
export interface HealthItem {
  id: string;
  targetId: string;
  targetName: string;
  check: string;
  status: "ok" | "repaired" | "warning" | "error";
  message: string;
  suggestion: string;
  repairable: boolean;
  repairId: string;
}
export interface HealthReport {
  startedAt: string;
  completedAt: string;
  trigger: "startup" | "manual" | "operation";
  full: boolean;
  items: HealthItem[];
}
export interface MemoryUsage {
  workingSetBytes: number;
  managerBytes: number;
  webViewBytes: number;
  privateBytes: number;
  processCount: number;
  sampledAt: string;
  partial: boolean;
  error: string;
}
export interface Snapshot {
  settings: Settings;
  sidebar: SidebarItem[];
  siteView: SiteViewPreferences;
  packages: Package[];
  instances: Instance[];
  sites: Site[];
  tasks: Task[];
  diagnostics: Diagnostic[];
  healthReport?: HealthReport | null;
  memoryUsage?: MemoryUsage | null;
  updateInfo?: UpdateInfo | null;
}
export type Page =
  | "overview"
  | "packages"
  | "instances"
  | "databases"
  | "sites"
  | "health"
  | "logs"
  | "settings";
export const kindName: Record<Kind, string> = {
  php: "PHP",
  mysql: "MySQL",
  nginx: "Nginx",
  redis: "Redis",
};
export const defaultPort: Record<Kind, number> = {
  php: 9100,
  mysql: 3307,
  nginx: 8081,
  redis: 6380,
};
export const stateName: Record<string, string> = {
  running: "运行中",
  stopped: "已停止",
  starting: "启动中",
  stopping: "停止中",
  error: "异常",
  pending: "等待中",
  queued: "排队中",
  completed: "已完成",
  success: "已完成",
  succeeded: "已完成",
  failed: "失败",
  cancelled: "已取消",
};
export function isActiveTask(task: Task) {
  return ["pending", "queued", "running"].includes(task.state);
}
export function progressPercent(task: Task) {
  return Math.min(100, Math.max(0, task.progress));
}
export function formatBytes(value: number) {
  if (!value) return "0 B";
  const index = Math.min(3, Math.floor(Math.log(value) / Math.log(1024)));
  return `${(value / 1024 ** index).toFixed(index ? 1 : 0)} ${["B", "KB", "MB", "GB"][index]}`;
}
