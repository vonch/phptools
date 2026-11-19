<script setup lang="ts">
import { computed, h, onMounted, ref, watch } from "vue";
import { NButton, NCard, NDataTable, NEmpty, NForm, NFormItem, NInput, NSpace, NTag, type DataTableColumns } from "naive-ui";
import { api } from "../api";
import { useAction } from "../composables";
import { snapshot, tasks } from "../state";
import { isActiveTask, type SiteCertificate } from "../types";

const { busy, run } = useAction();
const email = ref("");
const rows = ref<SiteCertificate[]>([]);
const loaded = ref(false);
const kindName: Record<SiteCertificate["kind"], string> = {
  none: "未申请",
  local: "本地证书",
  public: "公网证书",
  invalid: "证书无效",
};
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
const columns = computed<DataTableColumns<SiteCertificate>>(() => [
  { title: "站点", key: "siteName", ellipsis: { tooltip: true } },
  {
    title: "域名",
    key: "domains",
    render: (row) => h("div", [
      h("div", row.domains.join("、") || "未填写"),
      row.reason ? h("div", { class: "muted" }, row.reason) : null,
    ]),
  },
  {
    title: "当前证书",
    key: "kind",
    width: 120,
    render: (row) => h(NTag, { size: "small", bordered: false, type: kindType[row.kind] }, { default: () => kindName[row.kind] }),
  },
  {
    title: "到期",
    key: "notAfter",
    width: 120,
    render: (row) => row.notAfter ? row.notAfter.slice(0, 10) : "—",
  },
  {
    title: "操作",
    key: "actions",
    width: 320,
    render: (row) => {
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
]);
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
  void run(() => api.ImportPublicCertificate(row.siteId));
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
  <NCard title="公网证书" :bordered="false" class="panel">
    <p class="muted">
      为站点域名申请 Let’s Encrypt 证书。域名需要解析到这台电脑，并且公网可以访问 80 端口。也可以添加已经申请好的证书文件和未加密私钥。证书需要包含站点域名。添加或申请成功后，站点会改用这张证书提供 HTTPS。
    </p>
    <NForm label-placement="left" class="section-gap">
      <NFormItem label="通知邮箱">
        <NInput v-model:value="email" placeholder="用于接收证书到期通知" style="max-width: 320px" />
      </NFormItem>
    </NForm>
    <NDataTable v-if="rows.length" :columns="columns" :data="rows" :bordered="false" />
    <NEmpty v-else-if="loaded" description="还没有站点。请先在站点管理中创建。" />
  </NCard>
</template>
