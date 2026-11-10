<script setup lang="ts">
import { computed, h, ref, watch } from "vue";
import {
  NAlert,
  NButton,
  NCard,
  NDataTable,
  NEmpty,
  NForm,
  NFormItem,
  NIcon,
  NInput,
  NInputNumber,
  NModal,
  NSelect,
  NSpace,
  NTag,
  NTooltip,
  type DataTableColumns,
} from "naive-ui";
import { AddOutline, PlayOutline, StopOutline } from "@vicons/ionicons5";
import { api } from "../api";
import { compareServiceInstances } from "../serviceOrder";
import { useAction } from "../composables";
import { instances, packages, viewLog } from "../state";
import {
  defaultPort,
  kindName,
  stateName,
  type Instance,
  type InstanceInput,
} from "../types";
const { busy, run, confirm } = useAction();
const createModal = ref(false);
const configModal = ref(false);
const configInstance = ref<Instance | null>(null);
const configContent = ref("");
const selectedKind = ref("all");
const input = ref<InstanceInput>({
  name: "",
  packageId: "",
  port: 9100,
  password: "",
});
const installed = computed(() => packages.value.filter((pkg) => pkg.path));
const packageOptions = computed(() =>
  installed.value.map((pkg) => ({
    label: `${kindName[pkg.kind]} ${pkg.version}`,
    value: pkg.id,
  })),
);
const selectedPackage = computed(() =>
  installed.value.find((pkg) => pkg.id === input.value.packageId),
);
const filtered = computed(() =>
  instances.value
    .filter(
      (instance) =>
        selectedKind.value === "all" || instance.kind === selectedKind.value,
    )
    .map((instance) => ({
      instance,
      version: packages.value.find((pkg) => pkg.id === instance.packageId)?.version,
    }))
    .sort((left, right) =>
      compareServiceInstances(
        { kind: left.instance.kind, name: left.instance.name, version: left.version },
        { kind: right.instance.kind, name: right.instance.name, version: right.version },
      ),
    )
    .map((item) => item.instance),
);
watch(
  () => input.value.packageId,
  (id) => {
    const pkg = installed.value.find((item) => item.id === id);
    if (!pkg) return;
    let port = defaultPort[pkg.kind];
    while (instances.value.some((instance) => instance.port === port)) port++;
    input.value.port = port;
    input.value.name = `${kindName[pkg.kind]} ${pkg.version}`;
    input.value.password = "";
  },
);
function openCreate() {
  input.value = { name: "", packageId: "", port: 9100, password: "" };
  createModal.value = true;
}
function startAll() {
  void run(() => api.StartAll());
}
function stopAll() {
  void run(() => api.StopAll());
}
async function create() {
  await run(async () => {
    await api.CreateInstance({ ...input.value });
    input.value.password = "";
    createModal.value = false;
  }, "实例已创建");
}
async function editConfig(instance: Instance) {
  await run(async () => {
    configContent.value = await api.ReadConfig(instance.id);
    configInstance.value = instance;
    configModal.value = true;
  });
}
async function saveConfig() {
  if (configInstance.value)
    await run(async () => {
      await api.SaveConfig(configInstance.value!.id, configContent.value);
      configModal.value = false;
    }, "配置已保存");
}
function remove(instance: Instance) {
  confirm(
    `删除实例「${instance.name}」`,
    "删除前请先停止服务。实例记录将被移除，数据库、实例文件和 Windows 凭据会保留。",
    () => api.DeleteInstance(instance.id),
  );
}
function action(label: string, callback: () => void, disabled = false) {
  return h(
    NButton,
    {
      size: "tiny",
      secondary: true,
      disabled: busy.value || disabled,
      onClick: callback,
    },
    { default: () => label },
  );
}
const columns: DataTableColumns<Instance> = [
  {
    title: "服务实例",
    key: "name",
    minWidth: 200,
    render: (row) =>
      h("div", { class: "table-instance" }, [
        h(
          "div",
          { class: `component-avatar small ${row.kind}` },
          row.kind[0]?.toUpperCase(),
        ),
        h("div", null, [
          h("strong", null, row.name),
          h(
            "div",
            { class: "muted table-subtitle" },
            `${kindName[row.kind]} ${packages.value.find((pkg) => pkg.id === row.packageId)?.version ?? ""}`,
          ),
        ]),
      ]),
  },
  {
    title: "状态",
    key: "state",
    width: 110,
    render: (row) =>
      h(
        NTooltip,
        { disabled: !row.error },
        {
          trigger: () =>
            h(
              NTag,
              {
                size: "small",
                bordered: false,
                type:
                  row.state === "running"
                    ? "success"
                    : row.state === "error"
                      ? "error"
                      : "default",
              },
              { default: () => stateName[row.state] || row.state },
            ),
          default: () => row.error,
        },
      ),
  },
  {
    title: "端口",
    key: "port",
    width: 85,
    render: (row) => h("span", { class: "mono" }, row.port),
  },
  { title: "PID", key: "pid", width: 75, render: (row) => row.pid || "—" },
  {
    title: "操作",
    key: "actions",
    minWidth: 290,
    render: (row) =>
      h(
        NSpace,
        { size: 6 },
        {
          default: () => [
            action(
              row.state === "running" ? "停止" : "启动",
              () => {
                void run(() =>
                  row.state === "running"
                    ? api.StopInstance(row.id)
                    : api.StartInstance(row.id),
                );
              },
              ["starting", "stopping"].includes(row.state),
            ),
            action(
              "重启",
              () => {
                void run(() => api.RestartInstance(row.id));
              },
              row.state !== "running",
            ),
            action("配置", () => {
              void editConfig(row);
            }),
            action("日志", () => viewLog("instance", row.id)),
            h(
              NButton,
              {
                size: "tiny",
                quaternary: true,
                type: "error",
                disabled:
                  busy.value ||
                  row.state === "running" ||
                  ["starting", "stopping"].includes(row.state),
                onClick: () => remove(row),
              },
              { default: () => "删除" },
            ),
          ],
        },
      ),
  },
];
</script>

<template>
  <div class="page-heading">
    <h1>服务实例</h1>
    <div class="heading-actions">
      <NButton :disabled="!instances.length || busy" @click="stopAll"
        ><template #icon
          ><NIcon><StopOutline /></NIcon></template
        >全部停止</NButton
      ><NButton :disabled="!instances.length || busy" @click="startAll"
        ><template #icon
          ><NIcon><PlayOutline /></NIcon></template
        >全部启动</NButton
      ><NButton type="primary" :disabled="!installed.length" @click="openCreate"
        ><template #icon
          ><NIcon><AddOutline /></NIcon></template
        >创建实例</NButton
      >
    </div>
  </div>
  <NCard :bordered="false" class="panel"
    ><div class="table-toolbar">
      <div class="section-label">
        全部实例
        <NTag size="small" :bordered="false">{{ instances.length }}</NTag>
      </div>
      <NSelect
        v-model:value="selectedKind"
        :options="[
          { label: '全部类型', value: 'all' },
          ...Object.entries(kindName).map(([value, label]) => ({
            value,
            label,
          })),
        ]"
        style="width: 140px"
      />
    </div>
    <NDataTable
      v-if="instances.length"
      :columns="columns"
      :data="filtered"
      :bordered="false"
      :scroll-x="840"
      :row-key="(row: Instance) => row.id"
    /><NEmpty
      v-else
      :description="
        installed.length ? '还没有服务实例' : '请先到组件中心安装所需版本'
      "
      class="empty-block"
      ><template #extra
        ><NButton
          v-if="installed.length"
          type="primary"
          secondary
          @click="openCreate"
          >创建第一个实例</NButton
        ></template
      ></NEmpty
    ></NCard
  >
  <p class="footnote">
    PHPTools 只管理自身创建的实例。多版本 MySQL
    使用独立数据目录，请勿直接在版本之间复用数据。
  </p>
  <NModal
    v-model:show="createModal"
    preset="card"
    title="创建服务实例"
    class="form-modal"
    :mask-closable="false"
    @after-leave="input.password = ''"
    ><NForm label-placement="top"
      ><NFormItem label="组件版本" required
        ><NSelect
          v-model:value="input.packageId"
          :options="packageOptions"
          placeholder="选择已安装的组件版本" /></NFormItem
      ><NFormItem label="实例名称" required
        ><NInput
          v-model:value="input.name"
          placeholder="例如：项目 A · PHP 8.3"
          :maxlength="80" /></NFormItem
      ><NFormItem label="监听端口" required
        ><NInputNumber
          v-model:value="input.port"
          :min="1"
          :max="65535"
          :precision="0"
          style="width: 100%" /></NFormItem
      ><NFormItem
        v-if="selectedPackage?.kind === 'mysql'"
        label="MySQL root 密码"
        required
        ><NInput
          v-model:value="input.password"
          type="password"
          show-password-on="click"
          placeholder="至少 8 个字符，保存至 Windows 凭据管理器"
          autocomplete="new-password" /></NFormItem></NForm
    ><NAlert type="info" :show-icon="false"
      >实例默认只监听 127.0.0.1。MySQL 首次初始化可能需要一点时间。</NAlert
    ><template #footer
      ><NSpace justify="end"
        ><NButton :disabled="busy" @click="createModal = false">取消</NButton
        ><NButton
          type="primary"
          :loading="busy"
          :disabled="
            !input.name.trim() ||
            !input.packageId ||
            !input.port ||
            (selectedPackage?.kind === 'mysql' && input.password.length < 8)
          "
          @click="create"
          >创建实例</NButton
        ></NSpace
      ></template
    ></NModal
  >
  <NModal
    v-model:show="configModal"
    preset="card"
    :title="`实例配置 · ${configInstance?.name ?? ''}`"
    class="editor-modal"
    :mask-closable="false"
    ><p class="muted mono file-path">{{ configInstance?.configPath }}</p>
    <NAlert type="info" :show-icon="false" class="section-gap"
      >保存后按组件要求重启生效。Nginx
      配置会先进行语法校验，失败时恢复上一份有效配置。</NAlert
    ><NInput
      v-model:value="configContent"
      type="textarea"
      :autosize="{ minRows: 15, maxRows: 22 }"
      class="code-input"
      spellcheck="false"
    /><template #footer
      ><NSpace justify="end"
        ><NButton @click="configModal = false">关闭</NButton
        ><NButton type="primary" :loading="busy" @click="saveConfig"
          >保存配置</NButton
        ></NSpace
      ></template
    ></NModal
  >
</template>
