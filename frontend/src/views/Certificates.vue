<script setup lang="ts">
import { computed, h, onMounted, ref, watch } from "vue";
import {
  NButton,
  NCard,
  NCheckbox,
  NDataTable,
  NEmpty,
  NForm,
  NFormItem,
  NIcon,
  NInput,
  NModal,
  NPopover,
  NSpace,
  NTag,
  type DataTableBaseColumn,
  type DataTableColumns,
  type DataTableSortState,
} from "naive-ui";
import { OptionsOutline, SearchOutline } from "@vicons/ionicons5";
import { api } from "../api";
import {
  certificateKindName,
  defaultCertificateView,
  matchingCertificates,
  nextCertificateSort,
  orderedCertificates,
} from "../certificateTable";
import { useAction } from "../composables";
import { defaultSiteView, orderedSites } from "../siteTable";
import { sites, snapshot, tasks } from "../state";
import { isActiveTask, type CertificateSortableKey, type CertificateViewPreferences, type SiteCertificate } from "../types";

const { busy, run } = useAction();
const email = ref("");
const showImport = ref(false);
const importSiteID = ref("");
const certificateText = ref("");
const privateKeyText = ref("");
const rows = ref<SiteCertificate[]>([]);
const loaded = ref(false);
const search = ref("");
const showColumns = ref(false);
const hiddenColumnsDraft = ref<string[]>([]);
const preferences = computed(() => snapshot.value?.certificateView ?? defaultCertificateView());
const columnOptions = [
  { key: "siteName", label: "站点", width: 180, minWidth: 120 },
  { key: "domains", label: "域名", width: 260, minWidth: 140 },
  { key: "kind", label: "当前证书", width: 120, minWidth: 100 },
  { key: "notAfter", label: "到期", width: 120, minWidth: 100 },
  { key: "actions", label: "操作", width: 320, minWidth: 240 },
];
const kindType = {
  none: "default",
  local: "warning",
  public: "success",
  invalid: "error",
} as const;
const certificateTask = computed(() =>
  tasks.value.find((task) =>
    isActiveTask(task) && (task.action === "public-certificate" || task.action === "import-certificate" || task.action === "local-certificate"),
  ),
);
function columnSize(column: typeof columnOptions[number]) {
  return { width: column.width, minWidth: column.minWidth };
}
function sortableColumn(key: CertificateSortableKey, label: string): DataTableBaseColumn<SiteCertificate> {
  const order = preferences.value.sortBy === key
    ? (preferences.value.sortDirection === "asc" ? "ascend" : "descend")
    : false;
  const hint = order === "ascend" ? `${label}升序，点击切换降序`
    : order === "descend" ? `${label}降序，点击恢复站点列表顺序`
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
const manualIds = computed(() =>
  orderedSites(sites.value, snapshot.value?.siteView ?? defaultSiteView()).map((site) => site.id),
);
const filtered = computed(() =>
  matchingCertificates(
    orderedCertificates(rows.value, preferences.value, manualIds.value),
    search.value,
  ),
);
async function savePreferences(value: CertificateViewPreferences) {
  if (busy.value) return false;
  return (await run(async () => {
    await api.SaveCertificateView(value);
    return true;
  })) === true;
}
function setColumnsOpen(value: boolean) {
  if (busy.value) return;
  if (value) hiddenColumnsDraft.value = [...preferences.value.hiddenColumns];
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
  if (key === "siteName" || key === "domains" || key === "kind" || key === "notAfter")
    await savePreferences(nextCertificateSort(preferences.value, key));
}
const columns = computed<DataTableColumns<SiteCertificate>>(() => [
  { ...sortableColumn("siteName", "站点"), ellipsis: { tooltip: true } },
  {
    ...sortableColumn("domains", "域名"),
    render: (row: SiteCertificate) => h("div", [
      h("div", row.domains.join("、") || "未填写"),
      row.reason ? h("div", { class: "muted" }, row.reason) : null,
    ]),
  },
  {
    ...sortableColumn("kind", "当前证书"),
    render: (row: SiteCertificate) => h(NTag, { size: "small", bordered: false, type: kindType[row.kind] }, { default: () => certificateKindName[row.kind] }),
  },
  {
    ...sortableColumn("notAfter", "到期"),
    render: (row: SiteCertificate) => row.notAfter ? row.notAfter.slice(0, 10) : "—",
  },
  {
    title: "操作",
    key: "actions",
    width: 320,
    render: (row: SiteCertificate) => {
      const active = certificateTask.value?.targetId === row.siteId;
      const locked = !!certificateTask.value || busy.value;
      return h(NSpace, { size: 8, wrap: false }, {
        default: () => [
          h(NButton, {
            size: "small",
            type: "primary",
            secondary: true,
            disabled: !row.eligible || locked,
            loading: active && certificateTask.value?.action === "public-certificate",
            onClick: () => apply(row),
          }, { default: () => (row.kind === "public" ? "重新申请" : "申请") }),
          h(NButton, {
            size: "small",
            disabled: row.domains.length === 0 || locked,
            loading: active && certificateTask.value?.action === "import-certificate",
            onClick: () => importCertificate(row),
          }, { default: () => "添加证书" }),
          row.kind === "public"
            ? h(NButton, {
              size: "small",
              disabled: locked,
              loading: active && certificateTask.value?.action === "local-certificate",
              onClick: () => useLocal(row),
            }, { default: () => "改回本地证书" })
            : null,
        ],
      });
    },
  },
].filter((column) =>
  !preferences.value.hiddenColumns.includes(String(column.key)),
).map((column) => {
  const option = columnOptions.find((item) => item.key === column.key)!;
  return { ...column, ...columnSize(option) };
}));
async function load() {
  if (!snapshot.value?.settings.root) {
    rows.value = [];
    loaded.value = true;
    return;
  }
  try {
    const page = await api.GetCertificates();
    rows.value = page.sites;
    if (!email.value) email.value = page.email;
  } finally {
    loaded.value = true;
  }
}
function apply(row: SiteCertificate) {
  if (!row.eligible || certificateTask.value || busy.value) return;
  void run(() => api.ApplyPublicCertificate(row.siteId, email.value.trim()));
}
function importCertificate(row: SiteCertificate) {
  if (row.domains.length === 0 || certificateTask.value || busy.value) return;
  importSiteID.value = row.siteId;
  certificateText.value = "";
  privateKeyText.value = "";
  showImport.value = true;
}
function closeImport() {
  showImport.value = false;
  certificateText.value = "";
  privateKeyText.value = "";
}
async function submitImport() {
  const siteID = importSiteID.value;
  const certificate = certificateText.value;
  const privateKey = privateKeyText.value;
  if (!siteID || !certificate.trim() || !privateKey.trim() || certificateTask.value || busy.value) return;
  const started = await run(() => api.ImportPublicCertificate(siteID, certificate, privateKey));
  if (started) closeImport();
}
function useLocal(row: SiteCertificate) {
  if (row.kind !== "public" || certificateTask.value || busy.value) return;
  void run(() => api.UseLocalCertificate(row.siteId));
}
onMounted(() => { void load(); });
watch(() => snapshot.value?.sites, () => { void load(); });
watch(certificateTask, (task, previous) => {
  if (previous && !task) void load();
});
</script>

<template>
  <div class="certificates-page">
  <NCard title="公网证书" :bordered="false" class="panel certificates-panel">
    <p class="muted certificate-intro">
      为站点域名申请 Let’s Encrypt 证书。域名需要解析到这台电脑，并且公网可以访问 80 端口。也可以在添加窗口里贴上已经申请好的公钥和未加密私钥。证书需要包含站点域名。添加或申请成功后，站点会改用这张证书提供 HTTPS。
    </p>
    <NForm label-placement="left" class="certificate-email">
      <NFormItem label="通知邮箱">
        <NInput v-model:value="email" placeholder="用于接收证书到期通知" style="max-width: 320px" />
      </NFormItem>
    </NForm>
    <div class="table-toolbar certificate-toolbar">
      <div class="section-label">
        全部证书 <NTag size="small" :bordered="false">{{ search.trim() ? `${filtered.length} / ${rows.length}` : rows.length }}</NTag>
      </div>
      <div class="certificate-toolbar-controls">
        <NButton
          text
          size="small"
          :disabled="busy || preferences.sortBy === 'manual'"
          title="按站点列表的当前顺序排列"
          @click="savePreferences({ ...preferences, sortBy: 'manual', sortDirection: 'asc' })"
        >{{ preferences.sortBy === 'manual' ? '站点列表顺序' : '恢复站点列表顺序' }}</NButton>
        <NInput
          v-model:value="search"
          placeholder="搜索站点、域名或证书"
          :input-props="{ 'aria-label': '搜索证书' }"
          clearable
          class="certificate-search"
        >
          <template #prefix><NIcon><SearchOutline /></NIcon></template>
        </NInput>
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
          <div class="certificate-column-settings">
            <strong>显示列</strong>
            <p class="muted">选择列表中显示的字段，至少保留一列。</p>
            <NCheckbox
              v-for="column in columnOptions"
              :key="column.key"
              :checked="!hiddenColumnsDraft.includes(column.key)"
              :disabled="busy || (visibleDraftCount === 1 && !hiddenColumnsDraft.includes(column.key))"
              @update:checked="(value) => setColumnVisible(column.key, value)"
            >{{ column.label }}</NCheckbox>
            <NButton
              text
              type="primary"
              :disabled="busy || !hiddenColumnsDraft.length"
              class="certificate-columns-reset"
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
    <div v-if="rows.length" class="certificate-table-frame">
      <NDataTable
        class="certificate-table"
        flex-height
        :theme-overrides="{ tdColorSorting: 'var(--n-td-color)', thColorSorting: 'var(--n-th-color)' }"
        :columns="columns"
        :data="filtered"
        :bordered="false"
        :scroll-x="tableWidth"
        :row-key="(row: SiteCertificate) => row.siteId"
        @update:sorter="cycleSort"
      >
        <template #empty>
          <NEmpty description="没有找到匹配的证书">
            <template #extra><NButton secondary @click="search = ''">清除搜索</NButton></template>
          </NEmpty>
        </template>
      </NDataTable>
    </div>
    <NEmpty v-else-if="loaded" description="还没有站点。请先在站点管理中创建。" class="empty-block" />
  </NCard>
  </div>
  <NModal
    :show="showImport"
    preset="card"
    title="添加证书"
    class="certificate-modal"
    :mask-closable="false"
    @update:show="(value) => { if (!value) closeImport(); }"
  >
    <p class="muted">
      公钥填写证书内容，从 -----BEGIN CERTIFICATE----- 开始，中间证书可以贴在后面。私钥填写未加密的 PEM。
    </p>
    <NForm label-placement="top">
      <NFormItem label="公钥" required>
        <NInput
          v-model:value="certificateText"
          type="textarea"
          placeholder="-----BEGIN CERTIFICATE-----"
          :autosize="{ minRows: 6, maxRows: 10 }"
          :input-props="{ spellcheck: false, autocomplete: 'off' }"
        />
      </NFormItem>
      <NFormItem label="私钥" required>
        <NInput
          v-model:value="privateKeyText"
          type="textarea"
          placeholder="-----BEGIN PRIVATE KEY-----"
          :autosize="{ minRows: 6, maxRows: 10 }"
          :input-props="{ spellcheck: false, autocomplete: 'off' }"
        />
      </NFormItem>
    </NForm>
    <template #footer>
      <NSpace justify="end">
        <NButton :disabled="busy" @click="closeImport">取消</NButton>
        <NButton
          type="primary"
          :loading="busy"
          :disabled="!certificateText.trim() || !privateKeyText.trim()"
          @click="submitImport"
        >添加</NButton>
      </NSpace>
    </template>
  </NModal>
</template>

<style scoped>
.certificates-page {
  flex: 1 1 auto;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}
.certificates-panel {
  flex: 1 1 auto;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}
.certificates-panel :deep(.n-card-header),
.certificate-intro,
.certificate-email,
.certificate-toolbar {
  flex: 0 0 auto;
}
.certificates-panel :deep(.n-card__content) {
  flex: 1 1 auto;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}
.certificate-intro {
  margin-top: 0;
}
.certificate-email {
  margin-bottom: 0;
}
.certificate-toolbar {
  flex-wrap: wrap;
  align-items: flex-start;
  padding-bottom: 12px;
  background: #fff;
}
.certificate-toolbar-controls {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  flex-wrap: wrap;
  gap: 10px;
  max-width: 100%;
}
.certificate-search {
  width: 245px;
  max-width: 100%;
}
.certificate-column-settings {
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding: 4px;
}
.certificate-column-settings p {
  margin: -4px 0 0;
  font-size: 12px;
  line-height: 1.6;
}
.certificate-columns-reset {
  align-self: flex-start;
  margin: 2px 0 4px;
  font-size: 12px;
}
.certificate-table-frame {
  flex: 1 1 auto;
  min-height: 0;
}
.certificate-table {
  height: 100%;
}
</style>
