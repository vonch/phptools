import * as binding from "../bindings/phptools/internal/desktop/app.js";
import type {
  DependencyReport,
  AutoStartStatus,
  DatabaseAccount,
  DatabaseAccountInput,
  DatabaseInfo,
  DatabasePrivilegeInput,
  ManagedDatabase,
  DatabaseTool,
  Instance,
  InstanceInput,
  RewritePreset,
  Site,
  SiteEntrySelection,
  SiteViewPreferences,
  SidebarItem,
  Snapshot,
  UpdateInfo,
} from "./types";

export interface FeedbackReport {
  kind: "bug" | "suggestion";
  title: string;
  version: string;
  steps: string;
  expected: string;
  actual: string;
  problem: string;
  proposal: string;
  contact: string;
  images: { data: string }[];
}

interface DesktopAPI {
  GetRuntimeDependencies(): Promise<DependencyReport>;
  OpenRuntimeInstaller(id: string): Promise<void>;
  GetState(): Promise<Snapshot>;
  GetAutoStart(): Promise<AutoStartStatus>;
  SetAutoStart(enabled: boolean): Promise<AutoStartStatus>;
  GetUpdateInfo(): Promise<UpdateInfo>;
  CheckForUpdate(): Promise<UpdateInfo>;
  InstallUpdate(): Promise<string>;
  SaveSidebar(items: SidebarItem[]): Promise<void>;
  SaveSiteView(preferences: SiteViewPreferences): Promise<void>;
  SelectDirectory(title: string): Promise<string>;
  SelectSiteEntry(projectRoot: string): Promise<SiteEntrySelection>;
  GetRewritePresets(): Promise<RewritePreset[] | null>;
  SetRoot(root: string): Promise<void>;
  StartInstalledPHP(): Promise<string>;
  RefreshCatalog(): Promise<string>;
  CheckEnvironment(): Promise<string>;
  RepairHealthItem(id: string): Promise<string>;
  InstallPackage(id: string): Promise<string>;
  CancelTask(id: string): Promise<void>;
  UninstallPackage(id: string): Promise<void>;
  CreateInstance(input: InstanceInput): Promise<Instance>;
  DeleteInstance(id: string): Promise<void>;
  StartInstance(id: string): Promise<string>;
  StopInstance(id: string): Promise<string>;
  RestartInstance(id: string): Promise<string>;
  ListDatabases(instanceID: string): Promise<DatabaseInfo[]>;
  ListManagedDatabases(instanceID: string): Promise<ManagedDatabase[]>;
  CreateManagedDatabase(instanceID: string, name: string, password: string): Promise<ManagedDatabase>;
  SyncManagedDatabase(id: string): Promise<ManagedDatabase>;
  DeleteManagedDatabase(id: string): Promise<void>;
  DetectDatabaseTools(): Promise<DatabaseTool[]>;
  SelectDatabaseClient(): Promise<string>;
  AddDatabaseTool(kind: string, target: string): Promise<DatabaseTool>;
  RemoveDatabaseTool(id: string): Promise<void>;
  OpenDatabaseTool(instanceID: string, database: string, toolID: string): Promise<void>;
  CreateDatabase(instanceID: string, name: string, password: string): Promise<void>;
  DropDatabase(instanceID: string, name: string): Promise<void>;
  ListDatabaseAccounts(instanceID: string): Promise<DatabaseAccount[]>;
  CreateDatabaseAccount(
    instanceID: string,
    input: DatabaseAccountInput,
  ): Promise<void>;
  ChangeDatabaseAccountPassword(
    instanceID: string,
    input: DatabaseAccountInput,
  ): Promise<void>;
  DropDatabaseAccount(
    instanceID: string,
    user: string,
    host: string,
  ): Promise<void>;
  DatabaseAccountGrants(
    instanceID: string,
    user: string,
    host: string,
  ): Promise<string[]>;
  GrantDatabasePrivileges(
    instanceID: string,
    input: DatabasePrivilegeInput,
  ): Promise<void>;
  RevokeDatabasePrivileges(
    instanceID: string,
    input: DatabasePrivilegeInput,
  ): Promise<void>;
  SelectSQLImport(): Promise<string>;
  SelectSQLExport(database: string): Promise<string>;
  ImportDatabase(
    instanceID: string,
    database: string,
    path: string,
  ): Promise<string>;
  ExportDatabase(
    instanceID: string,
    database: string,
    path: string,
  ): Promise<string>;
  StartAll(): Promise<string>;
  StopAll(): Promise<string>;
  SaveSite(site: Site): Promise<Site>;
  DeleteSite(id: string): Promise<void>;
  ReadConfig(instanceID: string): Promise<string>;
  SaveConfig(instanceID: string, content: string): Promise<void>;
  ReadLog(kind: string, id: string, lines: number): Promise<string>;
  OpenDirectory(path: string): Promise<void>;
  VisitSite(id: string): Promise<void>;
  OpenOfficialSite(): Promise<void>;
  SubmitFeedback(report: FeedbackReport): Promise<string>;
  Quit(): Promise<void>;
}

// Wails generates this module from the registered Go service.
export const api: DesktopAPI = {
  ...binding,
  GetRuntimeDependencies: async () => (await binding.GetRuntimeDependencies()) as DependencyReport,
  GetState: async () => (await binding.GetState()) as Snapshot,
  CreateInstance: async (input) =>
    (await binding.CreateInstance(input)) as Instance,
  ListDatabases: async (instanceID) =>
    (await binding.ListDatabases(instanceID)) ?? [],
  ListManagedDatabases: async (instanceID) =>
    (await binding.ListManagedDatabases(instanceID)) ?? [],
  CreateManagedDatabase: async (instanceID, name, password) =>
    (await binding.CreateManagedDatabase(instanceID, name, password)) as ManagedDatabase,
  SyncManagedDatabase: async (id) =>
    (await binding.SyncManagedDatabase(id)) as ManagedDatabase,
  DetectDatabaseTools: async () => (await binding.DetectDatabaseTools()) ?? [],
  ListDatabaseAccounts: async (instanceID) =>
    (await binding.ListDatabaseAccounts(instanceID)) ?? [],
  DatabaseAccountGrants: async (instanceID, user, host) =>
    (await binding.DatabaseAccountGrants(instanceID, user, host)) ?? [],
};
