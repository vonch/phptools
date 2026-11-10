<script setup lang="ts">
import { computed, h, onBeforeUnmount, ref, watch } from "vue";
import { SearchOutline } from "@vicons/ionicons5";
import {
  NAlert, NButton, NCard, NDataTable, NEmpty, NForm, NFormItem,
  NIcon, NInput, NModal, NSelect, NSpace, NTabPane, NTabs, NTag, NTooltip,
  type DataTableColumns, type DataTableSortState,
} from "naive-ui";
import { api } from "../api";
import { useAction } from "../composables";
import { compareNames } from "../tableSort";
import DatabaseToolsModal from "../components/DatabaseToolsModal.vue";
import { errorText, instances, navigate, packages, state, tasks } from "../state";
import {
  isActiveTask, stateName,
  type DatabaseAccount, type ManagedDatabase,
} from "../types";

type Modal = "" | "create-db" | "drop-db" | "import" | "create-account"
  | "password" | "drop-account" | "grants" | "privileges";
const { busy, run, message } = useAction();
const selectedID = ref("");
const tab = ref("databases");
const databases = ref<ManagedDatabase[]>([]);
const search = ref("");
const managedID = ref("");
const managerShow = ref(false);
const managerDatabase = ref("");
const accounts = ref<DatabaseAccount[]>([]);
const databaseError = ref("");
const accountError = ref("");
const databaseLoading = ref(false);
const accountLoading = ref(false);
const modal = ref<Modal>("");
const modalInstanceID = ref("");
const modalInstanceName = ref("");
const databaseName = ref("");
const confirmName = ref("");
const sqlPath = ref("");
const account = ref<DatabaseAccount | null>(null);
const accountUser = ref("");
const accountHost = ref("localhost");
const accountPassword = ref("");
const grants = ref<string[]>([]);
const grantsError = ref("");
const privilegeDatabase = ref("");
const privileges = ref<string[]>([]);
const privilegeAction = ref<"grant" | "revoke">("grant");
let loadGeneration = 0;
let accountGeneration = 0;
let disposed = false;
const pendingTransfers = new Set<string>();

const mysqlInstances = computed(() => instances.value.filter((item) => item.kind === "mysql"));
const installedMySQL = computed(() => packages.value.some((item) => item.kind === "mysql" && item.path));
const selected = computed(() => mysqlInstances.value.find((item) => item.id === selectedID.value));
const running = computed(() => selected.value?.state === "running");
const instanceOptions = computed(() => mysqlInstances.value.map((item) => ({
  label: `${item.name} · :${item.port} · ${stateName[item.state] || item.state}`,
  value: item.id,
})));
const instanceTasks = computed(() => tasks.value.filter((task) =>
  task.targetId === selectedID.value && isActiveTask(task),
));
const locked = computed(() => busy.value || !!instanceTasks.value.length || !running.value ||
  (tab.value === "accounts" && accountLoading.value));
const catalogBusy = computed(() => busy.value || !!instanceTasks.value.length || !selected.value);
const modalUnavailable = computed(() => modalInstanceID.value !== selectedID.value ||
  (modal.value !== "create-db" && !running.value));
const syncedDatabases = computed(() => databases.value.filter((item) => item.state === "synced"));
const searchTerm = computed(() => search.value.trim().toLowerCase());
type DatabaseSortKey = "name" | "createdAt";
const sortValue = ref("name:asc");
const sortOptions = [
  { label: "名称升序", value: "name:asc" }, { label: "名称降序", value: "name:desc" },
  { label: "创建时间升序", value: "createdAt:asc" }, { label: "创建时间降序", value: "createdAt:desc" },
];
const visibleDatabases = computed(() => {
  const [key, direction] = sortValue.value.split(":") as [DatabaseSortKey, "asc" | "desc"];
  const factor = direction === "desc" ? -1 : 1;
  return databases.value.filter((item) => item.name.toLowerCase().includes(searchTerm.value))
    .slice()
    .sort((left, right) => compareNames(left[key], right[key]) * factor);
});
const privilegeOptions = [
  "SELECT", "INSERT", "UPDATE", "DELETE", "CREATE", "ALTER", "INDEX", "DROP",
  "CREATE VIEW", "SHOW VIEW", "TRIGGER", "REFERENCES", "EVENT", "EXECUTE",
  "CREATE ROUTINE", "ALTER ROUTINE", "CREATE TEMPORARY TABLES", "LOCK TABLES",
].map((value) => ({ label: value, value }));
const modalTitles: Record<Exclude<Modal, "">, string> = {
  "create-db": "创建数据库", "drop-db": "删除数据库", import: "确认导入 SQL",
  "create-account": "创建账号", password: "修改账号密码", "drop-account": "删除账号",
  grants: "账号权限", privileges: "管理数据库权限",
};
const modalTitle = computed(() => modal.value ? modalTitles[modal.value] : "");
const accountLabel = computed(() => account.value ? `${account.value.user || "（匿名）"}@${account.value.host}` : "");
const canSubmit = computed(() => {
  if (busy.value || modalInstanceID.value !== selectedID.value) return false;
  if (modal.value !== "create-db" && modal.value !== "drop-db" && !running.value) return false;
  if (instanceTasks.value.length) return false;
  switch (modal.value) {
    case "create-db": return /^[A-Za-z0-9_]{1,32}$/.test(databaseName.value) &&
      !["root", "mysql", "information_schema", "performance_schema", "sys"].includes(databaseName.value.toLowerCase()) &&
      accountPassword.value.length >= 8 && accountPassword.value.length <= 1024 && !/[\u0000\r\n]/.test(accountPassword.value);
    case "drop-db": return confirmName.value === databaseName.value;
    case "import": return !!sqlPath.value;
    case "create-account": return !!accountUser.value.trim() && !!accountHost.value.trim() && accountPassword.value.length >= 8;
    case "password": return !!account.value && !account.value.protected && accountPassword.value.length >= 8;
    case "drop-account": return !!account.value && !account.value.protected && confirmName.value === accountLabel.value;
    case "privileges": return !!account.value && !account.value.protected && !!privilegeDatabase.value && !!privileges.value.length;
    default: return false;
  }
});

function closeModal() {
  modal.value = "";
  accountPassword.value = "";
  sqlPath.value = "";
  grants.value = [];
  grantsError.value = "";
}
function openModal(kind: Modal, database = "", target: DatabaseAccount | null = null, id = "") {
  if (!selected.value || busy.value || !!instanceTasks.value.length) return;
  if (kind !== "create-db" && kind !== "drop-db" && !running.value) return;
  managedID.value = id;
  modalInstanceID.value = selected.value.id;
  modalInstanceName.value = selected.value.name;
  databaseName.value = database;
  account.value = target ? { ...target } : null;
  accountUser.value = "";
  accountHost.value = "localhost";
  accountPassword.value = "";
  confirmName.value = "";
  privilegeDatabase.value = "";
  privileges.value = [];
  privilegeAction.value = "grant";
  grants.value = [];
  grantsError.value = "";
  sqlPath.value = "";
  modal.value = kind;
}
async function loadDatabases(id = selectedID.value) {
  if (id !== selectedID.value || !selected.value || disposed) return;
  const generation = ++loadGeneration;
  databaseError.value = "";
  if (!databases.value.length) databaseLoading.value = true;
  try {
    const rows = await api.ListManagedDatabases(id);
    if (generation !== loadGeneration || id !== selectedID.value || disposed) return;
    databases.value = rows ?? [];
  } catch (error) {
    if (generation !== loadGeneration || id !== selectedID.value || disposed) return;
    databaseError.value = errorText(error);
  } finally {
    if (generation === loadGeneration) databaseLoading.value = false;
  }
}
async function loadAccounts(id = selectedID.value) {
  if (id !== selectedID.value || !running.value || disposed) return;
  const generation = ++accountGeneration;
  accountError.value = "";
  if (!accounts.value.length) accountLoading.value = true;
  try {
    const rows = await api.ListDatabaseAccounts(id);
    if (generation !== accountGeneration || id !== selectedID.value || disposed) return;
    accounts.value = rows ?? [];
  } catch (error) {
    if (generation !== accountGeneration || id !== selectedID.value || disposed) return;
    accountError.value = errorText(error);
  } finally {
    if (generation === accountGeneration) accountLoading.value = false;
  }
}
function refreshLists() {
  if (tab.value === "accounts") void loadAccounts();
  else void loadDatabases();
}
watch(mysqlInstances, (value) => {
  if (!value.some((item) => item.id === selectedID.value)) selectedID.value = value[0]?.id || "";
}, { immediate: true });
watch(selectedID, () => { search.value = ""; });
watch([selectedID, () => selected.value?.state], () => {
  ++loadGeneration;
  ++accountGeneration;
  databases.value = [];
  accounts.value = [];
  databaseError.value = "";
  accountError.value = "";
  databaseLoading.value = false;
  accountLoading.value = false;
  closeModal();
  managerShow.value = false;
  if (selected.value) void loadDatabases();
  if (running.value && tab.value === "accounts") void loadAccounts();
}, { immediate: true });
watch(tab, (value) => {
  if (value === "accounts" && running.value) void loadAccounts();
});
let previousTransfers = new Set<string>();
watch(tasks, (value) => {
  const transfers = value.filter((task) => task.targetId === selectedID.value &&
    ["database-import", "database-export"].includes(task.action));
  const finished = transfers.filter((task) => !isActiveTask(task) &&
    (previousTransfers.has(task.id) || pendingTransfers.has(task.id)));
  for (const task of finished) pendingTransfers.delete(task.id);
  previousTransfers = new Set(transfers.filter(isActiveTask).map((task) => task.id));
  if (finished.length) void loadDatabases();
}, { immediate: true });
onBeforeUnmount(() => { disposed = true; ++loadGeneration; ++accountGeneration; });

function assertTarget(id: string) {
  if (id !== selectedID.value || !mysqlInstances.value.some((item) => item.id === id && item.state === "running"))
    throw new Error("目标 MySQL 实例已变化或停止，请重新选择后操作。");
}
function startSelected() {
  const id = selectedID.value;
  if (!id || busy.value || instanceTasks.value.length) return;
  void run(() => api.StartInstance(id));
}
function trackTransfer(taskID: string) {
  const task = tasks.value.find((item) => item.id === taskID);
  if (task && !isActiveTask(task)) void loadDatabases();
  else pendingTransfers.add(taskID);
}
async function chooseImport(row: ManagedDatabase) {
  if (locked.value || row.state !== "synced") return;
  const id = selectedID.value;
  await run(async () => {
    const path = await api.SelectSQLImport();
    if (!path || disposed) return;
    assertTarget(id);
    // The picker runs while busy; open the modal only after it returns.
    modalInstanceID.value = id;
    modalInstanceName.value = selected.value!.name;
    databaseName.value = row.name;
    sqlPath.value = path;
    modal.value = "import";
  });
}
async function exportDatabase(row: ManagedDatabase) {
  if (locked.value || row.state !== "synced") return;
  const id = selectedID.value;
  await run(async () => {
    const path = await api.SelectSQLExport(row.name);
    if (!path || disposed) return;
    assertTarget(id);
    trackTransfer(await api.ExportDatabase(id, row.name, path));
    state.taskPanel = true;
  });
}
async function viewGrants(row: DatabaseAccount) {
  if (locked.value) return;
  openModal("grants", "", row);
  const id = modalInstanceID.value;
  if (modal.value !== "grants") return;
  await run(async () => {
    assertTarget(id);
    try {
      const value = await api.DatabaseAccountGrants(id, row.user, row.host);
      if (!disposed && modal.value === "grants" && modalInstanceID.value === id &&
        account.value?.user === row.user && account.value.host === row.host)
        grants.value = value ?? [];
    } catch (error) {
      if (!disposed && modal.value === "grants" && modalInstanceID.value === id)
        grantsError.value = errorText(error);
      throw error;
    }
  });
}
async function submit() {
  if (!canSubmit.value) return;
  const kind = modal.value;
  const id = modalInstanceID.value;
  const database = databaseName.value;
  const target = account.value ? { ...account.value } : null;
  const input = {
    user: kind === "create-account" ? accountUser.value.trim() : target?.user || "",
    host: kind === "create-account" ? accountHost.value.trim() : target?.host || "",
    password: accountPassword.value,
  };
  const permissionInput = {
    user: target?.user || "", host: target?.host || "",
    database: privilegeDatabase.value, privileges: [...privileges.value],
  };
  const path = sqlPath.value;
  const permissionAction = privilegeAction.value;
  await run(async () => {
    if (kind === "create-db" || kind === "drop-db") {
      if (id !== selectedID.value) throw new Error("目标 MySQL 实例已变化，请重新选择后操作。");
    } else {
      assertTarget(id);
    }
    let notice = "";
    switch (kind) {
      case "create-db": {
        const created = await api.CreateManagedDatabase(id, database, accountPassword.value);
        if (created.state !== "synced") notice = created.error || "已保存在 PHPTools，尚未同步到本地 MySQL";
        break;
      }
      case "drop-db": await api.DeleteManagedDatabase(managedID.value); break;
      case "import":
        trackTransfer(await api.ImportDatabase(id, database, path));
        state.taskPanel = true;
        break;
      case "create-account": await api.CreateDatabaseAccount(id, input); break;
      case "password": await api.ChangeDatabaseAccountPassword(id, input); break;
      case "drop-account": await api.DropDatabaseAccount(id, input.user, input.host); break;
      case "privileges":
        if (permissionAction === "grant") await api.GrantDatabasePrivileges(id, permissionInput);
        else await api.RevokeDatabasePrivileges(id, permissionInput);
        break;
      default: return;
    }
    if (disposed) return;
    closeModal();
    if (kind === "create-db" && notice) message.warning(notice);
    else message.success(kind === "import" ? "导入任务已提交，可在后台任务中查看进度" : kind === "create-db" ? "已创建并同步到本地 MySQL" : "操作已完成");
    if (kind === "create-db" || kind === "drop-db") await loadDatabases(id);
    if (["create-account", "password", "drop-account", "privileges"].includes(kind)) await loadAccounts(id);
  });
}
function action(label: string, callback: () => void, disabled = false, danger = false) {
  return h(NButton, {
    size: "tiny", secondary: true, disabled: (tab.value === "accounts" ? locked.value : catalogBusy.value) || disabled,
    type: danger ? "error" : "default", onClick: callback,
  }, { default: () => label });
}
async function syncDatabase(row: ManagedDatabase) {
  if (catalogBusy.value || !running.value) return;
  await run(async () => {
    const updated = await api.SyncManagedDatabase(row.id);
    await loadDatabases(updated.instanceId);
    if (updated.state === "synced") message.success("已同步到本地 MySQL");
    else message.warning(updated.error || "尚未同步到本地 MySQL");
  });
}
function exportAction(row: ManagedDatabase) {
  const unsupported = /[^\x00-\x7f]/.test(row.name);
  const button = action("导出", () => { void exportDatabase(row); }, unsupported);
  if (!unsupported) return button;
  return h(NTooltip, null, {
    trigger: () => h("span", { style: { display: "inline-flex" } }, [button]),
    default: () => "Windows MySQL 客户端暂不支持非 ASCII 库名导出，请使用支持 Unicode 的外部客户端",
  });
}
function manageDatabase(row: ManagedDatabase) {
  if (locked.value || row.state !== "synced") return;
  managerDatabase.value = row.name;
  managerShow.value = true;
}
function applySort(sorter: DataTableSortState | DataTableSortState[] | null) {
  if (!sorter || Array.isArray(sorter) || sorter.order === false) {
    sortValue.value = "name:asc";
    return;
  }
  if (sorter.columnKey === "name" || sorter.columnKey === "createdAt")
    sortValue.value = `${sorter.columnKey}:${sorter.order === "descend" ? "desc" : "asc"}`;
}
const databaseStateName: Record<string, string> = { synced: "已同步", pending: "待同步", error: "同步失败" };
const databaseColumns = computed<DataTableColumns<ManagedDatabase>>(() => {
  const [key, direction] = sortValue.value.split(":");
  const order = direction === "desc" ? "descend" : "ascend";
  function sorted(column: DatabaseSortKey) {
    return { sorter: true, sortOrder: (key === column ? order : false) as "ascend" | "descend" | false };
  }
  return [
  { title: "数据库名", key: "name", minWidth: 160, ...sorted("name"),
    render: (row) => h("strong", { class: "mono" }, row.name) },
  { title: "账号", key: "user", minWidth: 160, render: (row) => h("span", { class: "mono" }, `${row.user}@${row.host}`) },
  { title: "密码", key: "password", minWidth: 160, render: (row) => h("span", { class: "mono" }, row.password || "—") },
  { title: "状态", key: "state", minWidth: 180, render: (row) => h(NSpace, { vertical: true, size: 2 }, { default: () => [
    h(NTag, { size: "small", bordered: false, type: row.state === "synced" ? "success" : row.state === "error" ? "error" : "warning" }, { default: () => databaseStateName[row.state] || row.state }),
    ...(row.error ? [h("span", { class: "muted" }, row.error)] : []),
  ] }) },
  { title: "创建时间", key: "createdAt", minWidth: 170, ...sorted("createdAt") },
  { title: "操作", key: "actions", minWidth: 280, render: (row) => h(NSpace, { size: 6 }, { default: () => [
    ...(row.state === "synced" ? [] : [action("同步", () => { void syncDatabase(row); }, !running.value)]),
    action("管理", () => manageDatabase(row), row.state !== "synced"),
    action("导入 SQL", () => { void chooseImport(row); }, row.state !== "synced"),
    row.state === "synced" ? exportAction(row) : action("导出", () => {}, true),
    action("删除", () => openModal("drop-db", row.name, null, row.id), false, true),
  ] }) },
];
});
const accountColumns: DataTableColumns<DatabaseAccount> = [
  { title: "用户名", key: "user", minWidth: 160, render: (row) => h(NSpace, { align: "center", size: 7 }, {
    default: () => [h("strong", { class: "mono" }, row.user || "（匿名）"), ...(row.protected ? [
      h(NTag, { size: "small", bordered: false }, { default: () => "受保护" }),
    ] : [])],
  }) },
  { title: "来源 Host", key: "host", minWidth: 160, render: (row) => h("span", { class: "mono" }, row.host) },
  { title: "操作", key: "actions", minWidth: 350, render: (row) => h(NSpace, { size: 6 }, {
    default: () => [
      action("查看权限", () => { void viewGrants(row); }),
      action("授权 / 撤权", () => openModal("privileges", "", row), row.protected || !syncedDatabases.value.length),
      action("修改密码", () => openModal("password", "", row), row.protected),
      action("删除", () => openModal("drop-account", "", row), row.protected, true),
    ],
  }) },
];
</script>

<template>
  <div class="page-heading">
    <h1>数据库管理</h1>
    <div class="heading-actions">
      <NButton :disabled="locked" :loading="tab === 'accounts' ? accountLoading : databaseLoading" @click="refreshLists()">刷新列表</NButton>
      <NButton v-if="tab === 'databases'" type="primary" :disabled="catalogBusy" @click="openModal('create-db')">创建数据库</NButton>
      <NButton v-else type="primary" :disabled="locked" @click="openModal('create-account')">创建账号</NButton>
    </div>
  </div>
  <NCard :bordered="false" class="panel database-panel">
    <NAlert v-if="running && instanceTasks.length" type="info" :show-icon="false" class="section-gap">
      此实例有后台任务正在执行，完成后可继续操作。
      <NButton text type="primary" @click="state.taskPanel = true">查看后台任务</NButton>
    </NAlert>
    <NTabs v-model:value="tab" type="line" animated>
      <template #suffix>
        <NSelect v-model:value="selectedID" :options="instanceOptions" placeholder="选择 MySQL 实例"
          aria-label="选择 MySQL 实例" class="database-instance-select"
          :disabled="busy || !!modal || managerShow || !mysqlInstances.length" />
      </template>
      <NTabPane v-if="selected" name="databases" tab="数据库">
        <div class="database-list-options">
          <div class="database-search-controls">
            <span class="muted">PHPTools 记录 ·
              {{ searchTerm ? `${visibleDatabases.length} / ${databases.length}` : databases.length }}</span>
            <NInput v-model:value="search" clearable placeholder="搜索数据库名"
              :input-props="{ 'aria-label': '搜索数据库名' }" class="database-search">
              <template #prefix><NIcon><SearchOutline /></NIcon></template>
            </NInput>
            <NSelect v-model:value="sortValue" :options="sortOptions" aria-label="数据库排序" class="database-sort" />
          </div>
          <NButton v-if="!running" type="primary" secondary size="small"
            :disabled="busy || !!instanceTasks.length || ['starting', 'stopping'].includes(selected?.state || '')"
            @click="startSelected">启动 MySQL 后同步</NButton>
        </div>
        <NAlert v-if="!running" type="info" :show-icon="false" class="section-gap">
          数据库先保存在 PHPTools 的环境数据中。当前 MySQL 未运行，启动后点「同步」才会写入本地数据库。
        </NAlert>
        <NAlert v-if="databaseError" type="error" title="读取数据库记录失败" class="section-gap">{{ databaseError }}</NAlert>
        <NDataTable v-if="!databaseError" :columns="databaseColumns" :data="visibleDatabases" :loading="databaseLoading"
          :bordered="false" :scroll-x="1080" :row-key="(row: ManagedDatabase) => row.id" @update:sorter="applySort">
          <template #empty>
            <NEmpty :description="databaseLoading ? '正在读取数据库记录…' : searchTerm ? '没有匹配的数据库' : '还没有数据库，点击上方创建'">
              <template v-if="!databaseLoading && searchTerm" #extra>
                <NButton size="small" @click="search = ''">清空搜索</NButton>
              </template>
            </NEmpty>
          </template>
        </NDataTable>
        <p class="footnote">列表来自 PHPTools 自己保存的记录，包含数据库名、同名账号和密码。创建时先写入这里，再同步到所选 MySQL。这里不会列出 MySQL 里由其他工具创建的库。</p>
      </NTabPane>
      <NTabPane v-if="running" name="accounts" tab="账号与权限">
        <NAlert v-if="accountError" type="error" title="读取账号失败" class="section-gap">{{ accountError }}</NAlert>
        <NDataTable v-if="!accountError" :columns="accountColumns" :data="accounts" :loading="accountLoading"
          :bordered="false" :scroll-x="720" :row-key="(row: DatabaseAccount) => JSON.stringify([row.user, row.host])">
          <template #empty><NEmpty :description="accountLoading ? '正在读取账号…' : '暂无账号'" /></template>
        </NDataTable>
        <p class="footnote">MySQL 按用户名与 Host 共同区分账号。root、mysql.* 内置账号及匿名账号受保护，只能查看权限。</p>
      </NTabPane>
    </NTabs>
    <NEmpty v-if="!installedMySQL && !mysqlInstances.length" description="请先安装 MySQL 组件" class="empty-block">
      <template #extra><NButton type="primary" secondary @click="navigate('packages')">前往组件中心</NButton></template>
    </NEmpty>
    <NEmpty v-else-if="!mysqlInstances.length" description="还没有 MySQL 实例" class="empty-block">
      <template #extra><NButton type="primary" secondary @click="navigate('instances')">创建 MySQL 实例</NButton></template>
    </NEmpty>
  </NCard>

  <DatabaseToolsModal v-if="selected" v-model:show="managerShow" :instance="selected" :database="managerDatabase" />

  <NModal :show="!!modal" preset="card" :title="modalTitle" class="database-modal"
    :mask-closable="false" :closable="!busy" :close-on-esc="!busy"
    @update:show="(show) => { if (!show && !busy) closeModal(); }">
    <p class="muted database-context">目标实例：<strong>{{ modalInstanceName }}</strong></p>
    <NAlert v-if="modalUnavailable" type="error" class="section-gap">目标实例已变化或停止，请关闭后重新选择。</NAlert>
    <NForm v-if="modal === 'create-db'" label-placement="top">
      <NFormItem label="数据库名" required><NInput v-model:value="databaseName" :disabled="busy"
        :maxlength="32" placeholder="字母、数字或下划线，同时作为登录账号" /></NFormItem>
      <NFormItem label="密码" required><NInput v-model:value="accountPassword" :disabled="busy" type="password"
        show-password-on="click" autocomplete="new-password" placeholder="至少 8 个字符" /></NFormItem>
      <NAlert type="info" :show-icon="false">只填写数据库名和密码。记录先保存在 PHPTools，再同步到本地 MySQL，并创建同名账号「{{ databaseName || '数据库名' }}@localhost」。</NAlert>
    </NForm>
    <template v-else-if="modal === 'drop-db'">
      <NAlert type="error" class="section-gap">将从 PHPTools 删除「{{ databaseName }}」的记录。{{ databases.find((item) => item.id === managedID)?.state === 'synced' ? '已同步的数据库和同名账号会同时从本地 MySQL 删除，且无法撤销。' : '这条记录尚未同步，不会改动 MySQL。' }}</NAlert>
      <NFormItem :label="`输入数据库名 ${databaseName} 确认删除`" required><NInput v-model:value="confirmName" :disabled="busy" placeholder="完整输入数据库名" /></NFormItem>
    </template>
    <template v-else-if="modal === 'import'">
      <p>初始目标数据库：<strong class="mono">{{ databaseName }}</strong></p>
      <p class="muted mono file-path">{{ sqlPath }}</p>
      <NAlert type="warning">仅支持 UTF-8 编码的 .sql 文件，支持 DELIMITER。SQL 文件可能包含 USE、建库、删表等语句，并可能影响其他数据库；请先确认文件内容并备份。执行失败或取消时，已执行的语句可能保留，无法自动回滚。</NAlert>
    </template>
    <NForm v-else-if="modal === 'create-account'" label-placement="top">
      <NFormItem label="用户名" required><NInput v-model:value="accountUser" :disabled="busy" :maxlength="32" placeholder="例如：project_user" /></NFormItem>
      <NFormItem label="来源 Host" required><NInput v-model:value="accountHost" :disabled="busy" :maxlength="255" placeholder="localhost" /></NFormItem>
      <NFormItem label="密码" required><NInput v-model:value="accountPassword" :disabled="busy" type="password"
        show-password-on="click" autocomplete="new-password" placeholder="至少 8 个字符" /></NFormItem>
      <NAlert type="info" :show-icon="false">新账号创建后不授予数据库权限。请按项目需要在“授权 / 撤权”中配置。Host 默认 localhost；% 表示任意来源，实际连接范围仍受实例监听地址限制。</NAlert>
    </NForm>
    <template v-else-if="modal === 'password'">
      <p>账号：<strong class="mono">{{ accountLabel }}</strong></p>
      <NFormItem label="新密码" required><NInput v-model:value="accountPassword" :disabled="busy" type="password"
        show-password-on="click" autocomplete="new-password" placeholder="至少 8 个字符" /></NFormItem>
      <p class="muted">修改后请同步更新使用此账号的项目连接配置。</p>
    </template>
    <template v-else-if="modal === 'drop-account'">
      <NAlert type="error" class="section-gap">将删除账号「{{ accountLabel }}」及其全部权限，使用此账号的项目将无法重新连接。数据库与表会保留。</NAlert>
      <NFormItem :label="`输入 ${accountLabel} 确认删除`" required><NInput v-model:value="confirmName" :disabled="busy" placeholder="完整输入用户名@Host" /></NFormItem>
    </template>
    <template v-else-if="modal === 'grants'">
      <p>账号：<strong class="mono">{{ accountLabel }}</strong></p>
      <NAlert v-if="grantsError" type="error" title="读取账号权限失败">{{ grantsError }}</NAlert>
      <NInput v-else :value="busy ? '正在读取权限…' : grants.join('\n\n') || '暂无权限记录'" type="textarea" readonly
        :autosize="{ minRows: 7, maxRows: 16 }" class="code-input" />
    </template>
    <NForm v-else-if="modal === 'privileges'" label-placement="top">
      <p>账号：<strong class="mono">{{ accountLabel }}</strong></p>
      <NFormItem label="数据库" required><NSelect v-model:value="privilegeDatabase" :disabled="busy"
        :options="syncedDatabases.map((item) => ({ label: item.name, value: item.name }))" placeholder="选择要管理权限的数据库" /></NFormItem>
      <NFormItem label="操作" required><NSelect v-model:value="privilegeAction" :disabled="busy"
        :options="[{ label: '授予所选权限', value: 'grant' }, { label: '撤销所选权限', value: 'revoke' }]" /></NFormItem>
      <NFormItem label="权限" required><NSelect v-model:value="privileges" :disabled="busy" multiple
        :options="privilegeOptions" placeholder="选择权限，可多选" /></NFormItem>
      <NAlert :type="privilegeAction === 'revoke' ? 'warning' : 'info'" :show-icon="false">
        {{ privilegeAction === 'revoke' ? '将撤销此账号在所选数据库上的所选权限；可能影响项目运行。全局或其他来源授予的权限仍可能生效。' : '仅授予所选数据库内的所选权限，范围为该库全部表，不授予转授权权限。' }}
      </NAlert>
    </NForm>
    <template #footer>
      <NSpace justify="end">
        <NButton :disabled="busy" @click="closeModal()">{{ modal === 'grants' ? '关闭' : '取消' }}</NButton>
        <NButton v-if="modal !== 'grants'" :type="modal === 'drop-db' || modal === 'drop-account' ||
          (modal === 'privileges' && privilegeAction === 'revoke') ? 'error' : 'primary'"
          :loading="busy" :disabled="!canSubmit" @click="submit">
          {{ modal === 'import' ? '执行导入' : modal === 'drop-db' || modal === 'drop-account' ? '确认删除' :
            modal === 'create-db' ? '创建' : modal === 'privileges' ? privilegeAction === 'grant' ? '授予权限' : '确认撤销权限' : '保存' }}
        </NButton>
      </NSpace>
    </template>
  </NModal>
</template>
