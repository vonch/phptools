<script setup lang="ts">
import { computed, h, ref, type Component } from "vue";
import {
  NAlert,
  NButton,
  NCard,
  NCheckbox,
  NDataTable,
  NEmpty,
  NForm,
  NFormItem,
  NIcon,
  NInput,
  NInputNumber,
  NModal,
  NPopover,
  NSelect,
  NSpace,
  NSwitch,
  NTag,
  type DataTableBaseColumn,
  type DataTableProps,
  type DataTableSortState,
} from "naive-ui";
import {
  AddOutline,
  CreateOutline,
  FolderOpenOutline,
  OpenOutline,
  OptionsOutline,
  SearchOutline,
  TrashOutline,
} from "@vicons/ionicons5";
import { api } from "../api";
import { useAction } from "../composables";
import { databaseNameFromSite, domainFromName, folderFromName, parseBatchSites } from "../siteDefaults";
import { httpsFormStatus } from "../siteHttps";
import { defaultSiteView, nextSiteSort, orderedSites } from "../siteTable";
import { errorText, instances, navigate, sites, snapshot } from "../state";
import type { RewritePreset, Site, SiteSortableKey, SiteViewPreferences } from "../types";
const { busy, run, confirm, message: notify } = useAction();
const show = ref(false);
const showBatch = ref(false);
const batchText = ref("");
const batchPort = ref(80);
const batchMode = ref<"static" | "php">("static");
const batchPHP = ref("");
const batchHTTPS = ref(false);
const batchForceHTTPS = ref(false);
const batchRewriteID = ref("__default__");
const search = ref("");
const showColumns = ref(false);
const hiddenColumnsDraft = ref<string[]>([]);
const preferences = computed(() => snapshot.value?.siteView ?? defaultSiteView());
const columnOptions = [
  { key: "name", label: "站点 / 域名", width: 220, minWidth: 140 },
  { key: "root", label: "项目目录", width: 260, minWidth: 120 },
  { key: "phpInstanceId", label: "运行环境", width: 130, minWidth: 100 },
  { key: "enabled", label: "启用", width: 72, minWidth: 64 },
  { key: "createdAt", label: "添加日期", width: 165, minWidth: 130 },
  { key: "lastActiveAt", label: "最近活动日期", width: 165, minWidth: 130 },
  { key: "actions", label: "操作", width: 148, minWidth: 136 },
];
const columnWidths = ref<Record<string, number>>({});
function columnSize(column: typeof columnOptions[number]) {
  const minWidth = column.minWidth;
  return { width: Math.max(columnWidths.value[column.key] ?? column.width, minWidth), minWidth };
}
function sortableColumn(key: SiteSortableKey, label: string): DataTableBaseColumn<Site> {
  const order = preferences.value.sortBy === key
    ? (preferences.value.sortDirection === "asc" ? "ascend" : "descend")
    : false;
  const hint = order === "ascend" ? `${label}升序，点击切换降序`
    : order === "descend" ? `${label}降序，点击恢复最近修改优先`
      : `点击按${label}升序排列`;
  return {
    key,
    title: () => h("span", { title: hint }, label),
    sorter: true,
    sortOrder: order,
  };
}
const visibleDraftCount = computed(() =>
  columnOptions.filter((column) => !hiddenColumnsDraft.value.includes(column.key)).length,
);
const tableWidth = computed(() =>
  columnOptions
    .filter((column) => !preferences.value.hiddenColumns.includes(column.key))
    .reduce((width, column) => width + columnSize(column).width, 0),
);
const resizeColumn: NonNullable<DataTableProps["onUnstableColumnResize"]> =
  (_width, limitedWidth, column, getColumnWidth) => {
    const widths = { ...columnWidths.value };
    for (const option of columnOptions) {
      if (preferences.value.hiddenColumns.includes(option.key)) continue;
      const width = getColumnWidth(option.key);
      if (width !== undefined) widths[option.key] = width;
    }
    widths[column.key] = limitedWidth;
    columnWidths.value = widths;
  };
async function savePreferences(value: SiteViewPreferences) {
  if (busy.value) return false;
  return (await run(async () => {
    await api.SaveSiteView(value);
    return true;
  })) === true;
}
function setColumnsOpen(value: boolean) {
  if (busy.value) return;
  if (value) {
    hiddenColumnsDraft.value = [...preferences.value.hiddenColumns];
  }
  showColumns.value = value;
}
function setColumnVisible(key: string, visible: boolean) {
  if (busy.value || (!visible && visibleDraftCount.value <= 1)) return;
  hiddenColumnsDraft.value = visible
    ? hiddenColumnsDraft.value.filter((column) => column !== key)
    : [...hiddenColumnsDraft.value, key];
}
async function saveColumns() {
  if (!visibleDraftCount.value) return;
  if (await savePreferences({
    ...preferences.value, hiddenColumns: [...hiddenColumnsDraft.value],
  }))
    showColumns.value = false;
}
async function cycleSort(sorter: DataTableSortState | DataTableSortState[] | null) {
  if (!sorter || Array.isArray(sorter)) return;
  const key = sorter.columnKey;
  if (key === "name" || key === "createdAt" || key === "lastActiveAt")
    await savePreferences(nextSiteSort(preferences.value, key));
}
const mode = ref("static");
const defaultPreset = "__default__";
const customPreset = "__custom__";
const rewritePresets = ref<RewritePreset[]>([]);
const rewritePresetID = ref(defaultPreset);
const presetsLoading = ref(false);
let rewriteTouched = false;
const emptySite = (): Site => ({
  id: "",
  name: "",
  domain: "",
  secondDomain: "",
  root: "",
  entry: "",
  port: 80,
  https: false,
  forceHttps: false,
  phpInstanceId: "",
  phpPathInfo: false,
  enabled: true,
  rewrite: "",
  createdAt: "",
  updatedAt: "",
  lastActiveAt: "",
});
const input = ref<Site>(emptySite());
const newProject = ref(true);
const domainTouched = ref(false);
const openedHttps = ref(false);
const httpsStatus = computed(() => httpsFormStatus(input.value, openedHttps.value));
const createDatabase = ref(false);
const databaseTouched = ref(false);
const databaseName = ref("");
const databasePassword = ref("");
const mysqlInstanceId = ref("");
const mysqlOptions = computed(() =>
  instances.value
    .filter((instance) => instance.kind === "mysql")
    .map((instance) => ({ label: instance.name, value: instance.id })),
);
const rewriteOptions = computed(() => [
  { label: "默认规则", value: defaultPreset },
  ...rewritePresets.value.map((preset) => ({
    label: preset.name,
    value: preset.id,
  })),
  { label: "自定义", value: customPreset },
]);
const selectedPreset = computed(() =>
  rewritePresets.value.find((preset) => preset.id === rewritePresetID.value),
);
function normalizedRules(rules: string) {
  return rules.replace(/\r\n?/g, "\n").trim();
}
function identifyPreset(rules: string) {
  const normalized = normalizedRules(rules);
  if (!normalized) return defaultPreset;
  return (
    rewritePresets.value.find(
      (preset) => normalizedRules(preset.rules) === normalized,
    )?.id ?? customPreset
  );
}
async function ensurePresets() {
  if (!rewritePresets.value.length) {
    presetsLoading.value = true;
    try {
      rewritePresets.value = (await api.GetRewritePresets()) ?? [];
    } catch (error) {
      notify.error(errorText(error));
    } finally {
      presetsLoading.value = false;
    }
  }
  if (!rewriteTouched) rewritePresetID.value = identifyPreset(input.value.rewrite);
}
function selectPreset(id: string) {
  rewriteTouched = true;
  rewritePresetID.value = id;
  if (id === defaultPreset) {
    input.value.rewrite = "";
    return;
  }
  const preset = rewritePresets.value.find((item) => item.id === id);
  if (!preset) return;
  input.value.rewrite = preset.rules;
  mode.value = "php";
}
function updateRules(value: string) {
  rewriteTouched = true;
  input.value.rewrite = value;
  rewritePresetID.value = identifyPreset(value);
}
const phpOptions = computed(() =>
  instances.value
    .filter((instance) => instance.kind === "php")
    .map((instance) => ({
      label: instance.name,
      value: instance.id,
    })),
);
const hasNginx = computed(() =>
  instances.value.some((instance) => instance.kind === "nginx"),
);
const filtered = computed(() =>
  orderedSites(sites.value, preferences.value).filter((site) =>
    `${site.name} ${site.domain} ${site.secondDomain ?? ""} ${site.root}`
      .toLowerCase()
      .includes(search.value.trim().toLowerCase()),
  ),
);
function updateName(value: string) {
  input.value.name = value;
  if (!input.value.id && !domainTouched.value) input.value.domain = domainFromName(value);
  if (createDatabase.value && !databaseTouched.value) databaseName.value = databaseNameFromSite(value);
}
function updateDomain(value: string) {
  input.value.domain = value;
  domainTouched.value = value !== domainFromName(input.value.name);
}
function updateSecondDomain(value: string) {
  input.value.secondDomain = value;
}
function setHttps(value: boolean) {
  input.value.https = value;
  if (!value) input.value.forceHttps = false;
}
function setPort(value: number | null) {
  input.value.port = value ?? 80;
  if (input.value.port === 443) input.value.forceHttps = false;
}
function setCreateDatabase(value: boolean) {
  createDatabase.value = value;
  if (!value) return;
  if (!databaseTouched.value) databaseName.value = databaseNameFromSite(input.value.name);
  if (!mysqlInstanceId.value && mysqlOptions.value.length === 1)
    mysqlInstanceId.value = mysqlOptions.value[0].value;
}
function openBatch() {
  batchText.value = "";
  batchPort.value = 80;
  batchMode.value = "static";
  batchPHP.value = phpOptions.value.length === 1 ? phpOptions.value[0].value : "";
  batchHTTPS.value = false;
  batchForceHTTPS.value = false;
  batchRewriteID.value = defaultPreset;
  showBatch.value = true;
  void ensurePresets();
}
function setBatchHttps(value: boolean) {
  batchHTTPS.value = value;
  if (!value) batchForceHTTPS.value = false;
}
function setBatchPort(value: number | null) {
  batchPort.value = value ?? 80;
  if (batchPort.value === 443) batchForceHTTPS.value = false;
}
function setBatchMode(value: "static" | "php") {
  batchMode.value = value;
  if (value === "php" && !batchPHP.value && phpOptions.value.length === 1) {
    batchPHP.value = phpOptions.value[0].value;
  }
}
async function saveBatch() {
  let drafts;
  try {
    drafts = parseBatchSites(batchText.value);
  } catch (error) {
    notify.error(errorText(error));
    return;
  }
  const rewrite = batchRewriteID.value === defaultPreset
    ? ""
    : rewritePresets.value.find((preset) => preset.id === batchRewriteID.value)?.rules ?? "";
  const created = await run(
    () => api.CreateSites(drafts.map((draft) => ({
      ...emptySite(),
      name: draft.name,
      domain: draft.domain,
      projectFolder: draft.projectFolder,
      port: batchPort.value,
      https: batchHTTPS.value,
      forceHttps: batchHTTPS.value && batchPort.value !== 443 && batchForceHTTPS.value,
      phpInstanceId: batchMode.value === "php" ? batchPHP.value : "",
      rewrite,
    }))),
    batchHTTPS.value
      ? `已创建 ${drafts.length} 个站点，并生成了本地 HTTPS 证书`
      : `已创建 ${drafts.length} 个站点`,
  );
  if (created) showBatch.value = false;
}
function open(site?: Site) {
  input.value = site ? { ...site } : emptySite();
  newProject.value = !site;
  domainTouched.value = !!site;
  openedHttps.value = !!site?.https;
  createDatabase.value = false;
  databaseTouched.value = false;
  databaseName.value = "";
  databasePassword.value = "";
  mysqlInstanceId.value = mysqlOptions.value.length === 1 ? mysqlOptions.value[0].value : "";
  mode.value = site?.phpInstanceId ? "php" : "static";
  rewriteTouched = false;
  rewritePresetID.value = identifyPreset(input.value.rewrite);
  show.value = true;
  void ensurePresets();
}
function setRoot(value: string) {
  if (value === input.value.root) return;
  input.value.root = value;
  input.value.entry = "";
}
async function chooseRoot() {
  const form = input.value;
  const root = form.root;
  const path = await run(() => api.SelectSiteProjectDirectory());
  if (path && input.value === form && input.value.root === root) setRoot(path);
}
async function chooseEntry() {
  const form = input.value;
  const root = form.root;
  if (!root.trim()) return;
  const selection = await run(() => api.SelectSiteEntry(root));
  if (input.value === form && input.value.root === root && selection?.selected)
    input.value.entry = selection.entry;
}
function resetEntry() {
  input.value.entry = "";
}
async function save() {
  const creatingDatabase = createDatabase.value;
  const creatingNew = !input.value.id && newProject.value;
  const notice = await run(async () => {
    const saved = await api.SaveSite({
      ...input.value,
      root: creatingNew ? "" : input.value.root,
      entry: creatingNew ? "" : input.value.entry,
      projectFolder: creatingNew ? folderFromName(input.value.name) : "",
      phpInstanceId: mode.value === "php" ? input.value.phpInstanceId : "",
      phpPathInfo: mode.value === "php" && !!input.value.phpPathInfo,
    });
    input.value = { ...input.value, ...saved };
    if (!creatingDatabase) {
      show.value = false;
      if (creatingNew && saved.https) return "站点已创建，静态首页和 HTTPS 证书已生成";
      if (creatingNew) return "站点已创建，静态首页已生成";
      return saved.https ? "站点已保存，HTTPS 证书已生成" : "站点配置已保存";
    }
    const created = await api.CreateManagedDatabase(
      mysqlInstanceId.value,
      databaseName.value.trim(),
      databasePassword.value,
    );
    if (created.state === "error") throw new Error(created.error || "数据库创建失败");
    show.value = false;
    const lead = creatingNew ? "站点已创建，静态首页已生成。" : "站点已保存。";
    return created.state === "pending"
      ? `${lead}数据库已记录，MySQL 启动后到数据库页同步。`
      : `${lead}数据库已创建`;
  });
  if (notice) notify.success(notice);
}
function remove(site: Site) {
  confirm(
    `删除站点「${site.name}」`,
    "会移除站点配置和 PHPTools 管理的 hosts 条目，项目文件保留。操作 hosts 时可能请求管理员权限。",
    () => api.DeleteSite(site.id),
  );
}
const action = (label: string, icon: Component, callback: () => void, danger = false) =>
  h(
    NButton,
    {
      size: "small",
      quaternary: true,
      type: danger ? "error" : "default",
      disabled: busy.value,
      title: label,
      "aria-label": label,
      onClick: callback,
    },
    { icon: () => h(NIcon, { size: 16 }, { default: () => h(icon) }) },
  );
const instanceNames = computed(() => {
  const names = new Map<string, string>();
  for (const instance of instances.value) names.set(instance.id, instance.name);
  return names;
});
function siteAddresses(site: Site) {
  const format = (domain: string) => {
    if (site.https && (site.port === 80 || site.port === 443)) return `https://${domain}`;
    if (site.https) return `https://${domain}（HTTP ${site.port}）`;
    return domain + (site.port === 80 ? "" : `:${site.port}`);
  };
  return [site.domain, site.secondDomain ?? ""].map((domain) => domain.trim()).filter(Boolean).map(format);
}
function formatSiteDate(value: string) {
  const time = Date.parse(value);
  if (!Number.isFinite(time)) return "";
  const date = new Date(time);
  const pad = (part: number) => String(part).padStart(2, "0");
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}`;
}
const allColumns = computed<DataTableBaseColumn<Site>[]>(() => [
  {
    ...sortableColumn("name", "站点 / 域名"),
    render: (site) => {
      const addresses = siteAddresses(site);
      return h("div", { class: "site-identity" }, [
        h("strong", { class: "site-name-label", title: site.name }, site.name),
        ...addresses.map((address) => h("span", { class: "site-domain muted mono", title: address }, address)),
      ]);
    },
  },
  {
    title: "项目目录",
    key: "root",
    render: (site) =>
      h(
        "span",
        { class: "site-path muted mono", title: site.root + (site.entry ? ` / ${site.entry}` : "") },
        site.root + (site.entry ? ` / ${site.entry}` : ""),
      ),
  },
  {
    title: "运行环境",
    key: "phpInstanceId",
    render: (site) => {
      const label = site.phpInstanceId
        ? (instanceNames.value.get(site.phpInstanceId) ?? "PHP 实例不可用")
        : "静态站点";
      return h(
        "span",
        { class: site.phpInstanceId ? "site-runtime" : "site-runtime static", title: label },
        label,
      );
    },
  },
  {
    title: "启用",
    key: "enabled",
    render: (site) =>
      h(NSwitch, {
        value: site.enabled,
        disabled: busy.value,
        size: "small",
        "onUpdate:value": (value) => {
          void run(() => api.SaveSite({ ...site, enabled: value }));
        },
      }),
  },
  {
    ...sortableColumn("createdAt", "添加日期"),
    render: (site) => renderSiteDate(site.createdAt,
      site.createdAt ? "首次添加到 PHPTools 的时间" : "旧版未记录该站点的添加时间", "未记录"),
  },
  {
    ...sortableColumn("lastActiveAt", "最近活动日期"),
    render: (site) => renderSiteDate(site.lastActiveAt, "根据站点访问日志中的最近一次 HTTP 请求", "暂无记录"),
  },
  {
    title: "操作",
    key: "actions",
    fixed: "right",
    className: "site-actions-cell",
    render: (site) =>
      h("div", { class: "site-actions" }, [
        action("访问", OpenOutline, () => {
          void run(() => api.VisitSite(site.id));
        }),
        action("编辑", CreateOutline, () => open(site)),
        action("目录", FolderOpenOutline, () => {
          void run(() => api.OpenDirectory(site.root));
        }),
        action("删除", TrashOutline, () => remove(site), true),
      ]),
  },
]);
function renderSiteDate(value: string, hint: string, emptyLabel: string) {
  const text = formatSiteDate(value);
  return text
    ? h("span", { class: "mono", title: hint }, text)
    : h("span", { class: "muted", title: hint }, emptyLabel);
}
const columns = computed(() =>
  allColumns.value.filter((column) =>
    !preferences.value.hiddenColumns.includes(String(column.key)),
  ).map((column) => {
    const option = columnOptions.find((option) => option.key === column.key)!;
    return {
      ...column,
      resizable: option.key !== "actions",
      ...columnSize(option),
    };
  }),
);
</script>

<template>
  <div class="sites-page">
  <div class="page-heading">
    <h1>站点</h1>
    <div class="heading-actions">
      <NButton :disabled="busy" @click="openBatch">批量建站</NButton>
      <NButton type="primary" @click="open()"
        ><template #icon
          ><NIcon><AddOutline /></NIcon></template
        >添加站点</NButton
      >
    </div>
  </div>
  <NAlert
    v-if="!hasNginx"
    type="info"
    title="先准备一个 Nginx 实例"
    class="section-gap"
    >站点通过 Nginx 提供访问，请先安装 Nginx 并创建实例。
    <NButton size="small" @click="navigate('instances')"
      >前往服务实例</NButton
    ></NAlert
  >
  <NCard :bordered="false" class="panel sites-panel"
    ><div class="table-toolbar site-toolbar">
      <div class="section-label">
        全部站点 <NTag size="small" :bordered="false">{{ search.trim() ? `${filtered.length} / ${sites.length}` : sites.length }}</NTag>
      </div>
      <div class="site-toolbar-controls">
        <NButton text size="small" :disabled="busy || preferences.sortBy === 'manual'"
          title="按站点配置的最近保存时间倒序排列"
          @click="savePreferences({ ...preferences, sortBy: 'manual', sortDirection: 'asc', siteOrder: [] })"
        >{{ preferences.sortBy === 'manual' ? '最近修改优先' : '恢复最近修改优先' }}</NButton>
        <NInput
          v-model:value="search"
          placeholder="搜索名称、域名或目录"
          :input-props="{ 'aria-label': '搜索站点' }"
          clearable
          class="site-search"
          ><template #prefix
            ><NIcon><SearchOutline /></NIcon></template
        ></NInput>
        <NPopover
          :show="showColumns"
          trigger="click"
          placement="bottom-end"
          :width="240"
          @update:show="setColumnsOpen"
        >
          <template #trigger>
            <NButton :disabled="busy" title="显示列" aria-label="显示列">
              <template #icon><NIcon><OptionsOutline /></NIcon></template>
            </NButton>
          </template>
          <div class="site-column-settings">
            <strong>显示列</strong>
            <p class="muted">选择列表中显示的字段，至少保留一列。</p>
            <NCheckbox
              v-for="column in columnOptions"
              :key="column.key"
              :checked="!hiddenColumnsDraft.includes(column.key)"
              :disabled="busy || (visibleDraftCount === 1 && !hiddenColumnsDraft.includes(column.key))"
              :aria-disabled="busy || (visibleDraftCount === 1 && !hiddenColumnsDraft.includes(column.key))"
              @update:checked="(value) => setColumnVisible(column.key, value)"
            >{{ column.label }}</NCheckbox>
            <NButton
              text
              type="primary"
              :disabled="busy || !hiddenColumnsDraft.length"
              class="site-columns-reset"
              @click="hiddenColumnsDraft = []"
            >恢复默认显示</NButton>
            <NSpace justify="end" :size="8">
              <NButton size="small" :disabled="busy" @click="showColumns = false">取消</NButton>
              <NButton size="small" type="primary" :loading="busy" :disabled="!visibleDraftCount || busy" @click="saveColumns">应用</NButton>
            </NSpace>
          </div>
        </NPopover>
      </div>
    </div>
    <div v-if="sites.length" class="site-table-frame">
    <NDataTable
      class="site-table"
      flex-height
      :theme-overrides="{ tdColorSorting: 'var(--n-td-color)', thColorSorting: 'var(--n-th-color)' }"
      :columns="columns"
      :data="filtered"
      :bordered="false"
      :scroll-x="tableWidth"
      :row-key="(site: Site) => site.id"
      @unstable-column-resize="resizeColumn"
      @update:sorter="cycleSort"
    >
      <template #empty>
        <NEmpty description="没有找到匹配的站点">
          <template #extra><NButton secondary @click="search = ''">清除搜索</NButton></template>
        </NEmpty>
      </template>
    </NDataTable>
    </div><NEmpty v-else description="还没有本地站点" class="empty-block"
      ><template #extra
        ><NButton type="primary" secondary @click="open()"
          >添加第一个站点</NButton
        ></template
      ></NEmpty
    ></NCard
  >
  <p class="footnote">
    站点配置先通过 Nginx 校验再加载。hosts 条目由 PHPTools
    单独标记管理，更新时可能请求管理员权限。
  </p>
  </div>
  <NModal
    v-model:show="show"
    preset="card"
    :title="input.id ? '编辑站点' : '添加本地站点'"
    class="site-modal"
    :mask-closable="false"
    ><NForm class="site-form" label-placement="top">
      <section class="site-form-section">
        <h3>站点</h3>
        <div class="form-grid">
          <NFormItem label="站点名称" required>
            <NInput
              :value="input.name"
              placeholder="例如：我的项目"
              :maxlength="80"
              @update:value="updateName"
            />
          </NFormItem>
          <NFormItem label="站点模式">
            <NSelect
              v-model:value="mode"
              :options="[
                { label: '静态站点', value: 'static' },
                { label: '通用 PHP 入口', value: 'php' },
              ]"
            />
          </NFormItem>
        </div>
        <div class="form-grid domain-grid">
          <NFormItem label="第一域名" required>
            <NInput
              :value="input.domain"
              placeholder="例如：project.test"
              @update:value="updateDomain"
            />
          </NFormItem>
          <NFormItem label="第二域名">
            <NInput
              :value="input.secondDomain || ''"
              placeholder="可选，例如 api.project.test"
              @update:value="updateSecondDomain"
            />
          </NFormItem>
          <NFormItem :label="input.https && input.port === 443 ? 'HTTPS 端口' : 'HTTP 端口'" required>
            <NInputNumber
              :value="input.port"
              :min="1"
              :max="65535"
              :precision="0"
              @update:value="setPort"
            />
          </NFormItem>
        </div>
        <div v-if="!input.id" class="site-switch-row">
          <div>
            <span class="site-switch-label">新项目</span>
            <p class="muted">在当前环境的 sites 目录下创建小写英文文件夹，不含空格和特殊符号，并生成一个表示创建成功的静态首页。</p>
          </div>
          <NSwitch v-model:value="newProject" />
        </div>
        <template v-if="input.id || !newProject">
        <NFormItem label="项目目录" required>
          <div class="input-action">
            <NInput
              :value="input.root"
              placeholder="选择项目所在文件夹"
              @update:value="setRoot"
            />
            <NButton :disabled="busy" @click="chooseRoot">
              <template #icon><NIcon><FolderOpenOutline /></NIcon></template>
              浏览
            </NButton>
          </div>
        </NFormItem>
        <NFormItem label="入口目录（相对项目目录）">
          <div class="site-entry-field">
            <div class="input-action site-entry-control">
              <NInput :value="input.entry || '.'" readonly />
              <NButton :disabled="!input.root.trim() || busy" @click="chooseEntry">
                <template #icon><NIcon><FolderOpenOutline /></NIcon></template>
                浏览
              </NButton>
              <NButton :disabled="!input.entry || busy" @click="resetEntry">使用根目录</NButton>
            </div>
            <p class="muted">点号表示项目根目录，其他路径相对于项目目录。</p>
          </div>
        </NFormItem>
        </template>
        <div class="site-switch-row">
          <span class="site-switch-label">启用站点</span>
          <NSwitch v-model:value="input.enabled" />
        </div>
        <div class="site-switch-row">
          <div>
            <span class="site-switch-label">HTTPS</span>
            <p class="muted">{{ httpsStatus.text }}</p>
            <p v-for="address in httpsStatus.addresses" :key="address" class="muted mono">{{ address }}</p>
          </div>
          <NSwitch v-model:value="input.https" @update:value="setHttps" />
        </div>
        <div v-if="input.https" class="site-switch-row">
          <div>
            <span class="site-switch-label">HTTP 强制跳转 HTTPS</span>
            <p class="muted">{{ input.port === 443 ? "当前端口已是 HTTPS，没有可跳转的 HTTP 端口。请把端口改为 80。" : "用 HTTP 访问时跳转到 HTTPS。证书验证地址仍然从 HTTP 提供。" }}</p>
          </div>
          <NSwitch v-model:value="input.forceHttps" :disabled="input.port === 443" />
        </div>
      </section>
      <section v-if="mode === 'php'" class="site-form-section">
        <h3>PHP</h3>
        <NFormItem label="PHP 服务实例" required>
          <NSelect
            v-model:value="input.phpInstanceId"
            :options="phpOptions"
            placeholder="选择 PHP 实例"
          />
        </NFormItem>
        <NAlert v-if="!phpOptions.length" type="warning">
          请先创建 PHP 实例，再将站点关联到对应版本。
        </NAlert>
        <div class="site-switch-row">
          <div>
            <span class="site-switch-label">PATH_INFO</span>
            <p class="muted">允许 /index.php/path 形式的地址，仅在项目需要时开启。</p>
          </div>
          <NSwitch v-model:value="input.phpPathInfo" />
        </div>
      </section>
      <section class="site-form-section">
        <div class="site-switch-row">
          <div>
            <span class="site-switch-label">创建数据库</span>
            <p class="muted">同时创建同名的 localhost 账号。</p>
          </div>
          <NSwitch :value="createDatabase" @update:value="setCreateDatabase" />
        </div>
        <template v-if="createDatabase">
          <NAlert v-if="!mysqlOptions.length" type="warning">
            还没有 MySQL 实例。请先创建 MySQL，或关闭创建数据库。
          </NAlert>
          <div v-else class="site-db-fields">
            <div class="form-grid">
              <NFormItem label="MySQL 实例" required>
                <NSelect
                  v-model:value="mysqlInstanceId"
                  :options="mysqlOptions"
                  placeholder="选择 MySQL 实例"
                />
              </NFormItem>
              <NFormItem label="数据库名" required>
                <NInput
                  :value="databaseName"
                  :maxlength="32"
                  placeholder="字母、数字或下划线"
                  @update:value="(value) => { databaseTouched = true; databaseName = value; }"
                />
              </NFormItem>
            </div>
            <NFormItem label="数据库密码" required>
              <NInput
                v-model:value="databasePassword"
                type="password"
                show-password-on="click"
                autocomplete="new-password"
                placeholder="至少 8 个字符"
              />
            </NFormItem>
            <p class="muted">保存后同步到所选 MySQL。MySQL 未运行时先记录，启动后到数据库页同步。</p>
          </div>
        </template>
      </section>
      <section class="site-form-section">
        <h3>访问</h3>
        <NFormItem label="伪静态预设">
          <NSelect
            :value="rewritePresetID"
            :options="rewriteOptions"
            :loading="presetsLoading"
            @update:value="selectPreset"
          />
        </NFormItem>
        <NAlert v-if="selectedPreset" type="info" :show-icon="false">
          {{ selectedPreset.description }}
          <p class="preset-entry-hint">预设用于独立域名下的根路径部署。</p>
          <p class="preset-entry-hint">
            建议入口目录：{{ selectedPreset.suggestedEntry || "项目根目录" }}。请按项目实际结构选择。
          </p>
        </NAlert>
        <p v-else class="muted">
          {{
            rewritePresetID === defaultPreset
              ? "使用与站点模式对应的默认访问规则。"
              : "按项目需要编辑规则，保存前会校验配置。"
          }}
        </p>
        <NFormItem label="伪静态规则">
          <NInput
            :value="input.rewrite"
            type="textarea"
            :autosize="{ minRows: 3, maxRows: 8 }"
            placeholder="可选。填写 server 内的完整伪静态配置，包含 location 块。"
            class="code-input"
            spellcheck="false"
            @update:value="updateRules"
          />
        </NFormItem>
        <p class="muted">保存时会更新 PHPTools 管理的 hosts 条目，Windows 可能请求管理员权限。</p>
      </section>
    </NForm>
    <template #footer
      ><NSpace justify="end"
        ><NButton :disabled="busy" @click="show = false">取消</NButton
        ><NButton
          type="primary"
          :loading="busy"
          :disabled="
            !input.name.trim() ||
            !input.domain.trim() ||
            ((!!input.id || !newProject) && !input.root.trim()) ||
            !input.port ||
            (mode === 'php' && !input.phpInstanceId) ||
            (createDatabase && (
              !mysqlOptions.length ||
              !mysqlInstanceId ||
              !databaseName.trim() ||
              databasePassword.length < 8
            ))
          "
          @click="save"
          >保存站点</NButton
        ></NSpace
      ></template
    ></NModal
  >
  <NModal
    v-model:show="showBatch"
    preset="card"
    title="批量建站"
    class="site-modal"
    :mask-closable="false"
  >
    <NForm label-placement="top">
      <NFormItem label="站点列表" required>
        <NInput
          v-model:value="batchText"
          type="textarea"
          placeholder="我的商店&#10;blog.test&#10;演示 demo.test"
          :autosize="{ minRows: 6, maxRows: 12 }"
          :input-props="{ spellcheck: false }"
        />
      </NFormItem>
      <p class="muted">每行一个站点，一次最多 50 个。只写名称时自动生成 .test 域名；指定域名时写成「名称 域名」。每个站点都会在 sites 目录生成项目文件夹和静态首页。</p>
      <div class="form-grid">
        <NFormItem label="站点模式">
          <NSelect
            :value="batchMode"
            :options="[
              { label: '静态站点', value: 'static' },
              { label: '通用 PHP 入口', value: 'php' },
            ]"
            @update:value="setBatchMode"
          />
        </NFormItem>
        <NFormItem label="HTTP 端口" required>
          <NInputNumber :value="batchPort" :min="1" :max="65535" :precision="0" @update:value="setBatchPort" />
        </NFormItem>
      </div>
      <NFormItem v-if="batchMode === 'php'" label="PHP 服务实例" required>
        <NSelect v-model:value="batchPHP" :options="phpOptions" placeholder="选择 PHP 实例" />
      </NFormItem>
      <NAlert v-if="batchMode === 'php' && !phpOptions.length" type="warning">请先创建 PHP 实例。</NAlert>
      <NFormItem label="伪静态">
        <NSelect
          v-model:value="batchRewriteID"
          :options="rewriteOptions.filter((option) => option.value !== customPreset)"
          :loading="presetsLoading"
        />
      </NFormItem>
      <div class="site-switch-row">
        <div>
          <span class="site-switch-label">HTTPS</span>
          <p class="muted">为每个站点生成本地 HTTPS 证书。</p>
        </div>
        <NSwitch :value="batchHTTPS" @update:value="setBatchHttps" />
      </div>
      <div v-if="batchHTTPS" class="site-switch-row">
        <div>
          <span class="site-switch-label">HTTP 强制跳转 HTTPS</span>
          <p class="muted">{{ batchPort === 443 ? "当前端口已是 HTTPS，没有可跳转的 HTTP 端口。请把端口改为 80。" : "用 HTTP 访问时跳转到 HTTPS。" }}</p>
        </div>
        <NSwitch v-model:value="batchForceHTTPS" :disabled="batchPort === 443" />
      </div>
    </NForm>
    <template #footer>
      <NSpace justify="end">
        <NButton :disabled="busy" @click="showBatch = false">取消</NButton>
        <NButton
          type="primary"
          :loading="busy"
          :disabled="!batchText.trim() || !batchPort || (batchMode === 'php' && !batchPHP)"
          @click="saveBatch"
        >创建</NButton>
      </NSpace>
    </template>
  </NModal>
</template>

<style scoped>
.sites-page {
  flex: 1 1 auto;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}
.sites-page > .page-heading,
.sites-page > .footnote,
.sites-page > .section-gap {
  flex-shrink: 0;
}
.site-form :deep(.n-form-item) {
  margin-bottom: 0;
}
.site-form :deep(.n-input-number) {
  width: 100%;
}
.site-form-section {
  display: flex;
  flex-direction: column;
  gap: 14px;
  margin-top: 16px;
  padding-top: 16px;
  border-top: 1px solid #e8edf0;
}
.site-form-section:first-child {
  margin-top: 0;
  padding-top: 0;
  border-top: 0;
}
.site-form-section h3 {
  margin: 0;
  font-size: 13px;
  font-weight: 650;
  color: #21313e;
}
.site-switch-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
}
.site-switch-label {
  font-size: 13px;
  color: #21313e;
}
.site-switch-row p,
.site-entry-field p,
.site-form-section > .muted,
.site-db-fields > .muted {
  margin: 2px 0 0;
}
.site-db-fields {
  display: flex;
  flex-direction: column;
  gap: 14px;
  padding: 14px;
  border: 1px solid #e8edf0;
  border-radius: 8px;
  background: #f7f9fa;
}
.sites-panel {
  flex: 1 1 auto;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}
.sites-panel :deep(.n-card__content) {
  flex: 1 1 auto;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}
.site-table-frame {
  flex: 1 1 auto;
  min-height: 0;
}
.site-table {
  height: 100%;
}
.site-actions {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  width: 100%;
  gap: 0;
  white-space: nowrap;
}
.site-actions :deep(.n-button) {
  width: 28px;
  height: 28px;
  padding: 0;
}
.site-runtime {
  display: inline-block;
  max-width: 100%;
  padding: 0 7px;
  border-radius: 3px;
  background: #e8f4ef;
  color: #187868;
  font-size: 12px;
  line-height: 20px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  vertical-align: bottom;
}
.site-runtime.static {
  background: #f1f3f5;
  color: #66717d;
}
.site-toolbar {
  flex: 0 0 auto;
  flex-wrap: wrap;
  align-items: flex-start;
  padding-bottom: 12px;
  background: #fff;
}
.site-toolbar > .section-label {
  min-height: 34px;
}
.site-toolbar-controls {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  flex-wrap: wrap;
  gap: 10px;
  max-width: 100%;
}
.site-search {
  width: 245px;
  max-width: 100%;
}
.site-column-settings {
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding: 4px;
}
.site-column-settings p {
  margin: -4px 0 0;
  font-size: 12px;
  line-height: 1.6;
}
.site-columns-reset {
  align-self: flex-start;
  margin: 2px 0 4px;
  font-size: 12px;
}
:deep(.site-identity) {
  display: flex;
  flex-direction: column;
  gap: 1px;
  min-width: 0;
  line-height: 1.4;
}
:deep(.site-domain),
:deep(.site-name-label),
:deep(.site-path) {
  display: block;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
:deep(.site-domain) {
  font-size: 12px;
}
.domain-grid {
  grid-template-columns: minmax(0, 1fr) minmax(0, 1fr) 150px;
}
@media (max-width: 640px) {
  .domain-grid {
    grid-template-columns: 1fr;
  }
}
@media (max-width: 900px) {
  .site-toolbar-controls {
    justify-content: flex-start;
    width: 100%;
  }
  .site-search {
    flex: 1 1 240px;
  }
}
</style>
