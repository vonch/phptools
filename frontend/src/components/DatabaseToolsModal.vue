<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from "vue";
import { Clipboard } from "@wailsio/runtime";
import {
  NAlert, NButton, NDivider, NEmpty, NFormItem, NInput, NModal,
  NSelect, NSpace, NSpin, NTag, type InputInst,
} from "naive-ui";
import { api } from "../api";
import { useAction } from "../composables";
import { errorText } from "../state";
import type { DatabaseTool, Instance } from "../types";

const props = defineProps<{ show: boolean; instance: Instance; database: string }>();
const emit = defineEmits<{ "update:show": [value: boolean] }>();
const { busy, run } = useAction();
const tools = ref<DatabaseTool[]>([]);
const loading = ref(false);
const detectionError = ref("");
const kind = ref("desktop");
const target = ref("");
const manualSection = ref<HTMLElement | null>(null);
const targetInput = ref<InputInst | null>(null);
let generation = 0;
let disposed = false;
const contextKey = computed(() => `${props.instance.id}:${props.instance.port}:${props.database}`);
const connectionText = computed(() =>
  `主机：127.0.0.1\n端口：${props.instance.port}\n用户名：root\n数据库：${props.database}`,
);
const kindOptions = [
  { label: "本机数据库客户端", value: "desktop" },
  { label: "phpMyAdmin 网页地址", value: "phpmyadmin" },
];

async function detect() {
  const current = ++generation;
  loading.value = true;
  detectionError.value = "";
  try {
    const found = await api.DetectDatabaseTools();
    if (!disposed && props.show && current === generation) tools.value = found;
  } catch (error) {
    if (!disposed && current === generation) detectionError.value = errorText(error);
  } finally {
    if (!disposed && current === generation) loading.value = false;
  }
}
watch(() => [props.show, contextKey.value], () => {
  ++generation;
  tools.value = [];
  detectionError.value = "";
  kind.value = "desktop";
  target.value = "";
  if (props.show) void detect();
}, { immediate: true });
onBeforeUnmount(() => { disposed = true; ++generation; });

function changeKind(value: string) {
  kind.value = value;
  target.value = "";
}
async function chooseClient() {
  const context = contextKey.value;
  const path = await run(() => api.SelectDatabaseClient());
  if (path && !disposed && props.show && context === contextKey.value) target.value = path;
}
async function addTool() {
  if (!target.value.trim()) return;
  await run(async () => {
    await api.AddDatabaseTool(kind.value, target.value.trim());
    target.value = "";
    await detect();
  }, "工具已添加");
}
async function removeTool(tool: DatabaseTool) {
  await run(async () => {
    await api.RemoveDatabaseTool(tool.id);
    await detect();
  });
}
async function openTool(tool: DatabaseTool) {
  const id = props.instance.id;
  const database = props.database;
  await run(() => api.OpenDatabaseTool(id, database, tool.id), "已打开工具，请按连接信息选择当前数据库");
}
async function configureAddress() {
  kind.value = "phpmyadmin";
  target.value = "";
  await nextTick();
  manualSection.value?.scrollIntoView({ block: "nearest" });
  targetInput.value?.focus();
}
function copyConnection() {
  void run(() => Clipboard.SetText(connectionText.value), "连接信息已复制");
}
</script>

<template>
  <NModal :show="show" preset="card" :title="`管理数据库 · ${database}`" class="database-tools-modal"
    :mask-closable="false" :closable="!busy" :close-on-esc="!busy"
    @update:show="(value) => { if (!busy) emit('update:show', value); }">
    <div class="database-tool-context">
      <div>
        <strong>{{ instance.name }}</strong>
        <p class="mono">127.0.0.1:{{ instance.port }} · {{ database }} · root</p>
      </div>
      <NButton size="small" :disabled="busy" @click="copyConnection">复制连接信息</NButton>
    </div>
    <NAlert type="info" :show-icon="false" class="section-gap">
      打开工具后，请确认连接到上方实例和端口，再选择当前数据库。首次连接需输入实例密码。
    </NAlert>
    <div class="table-toolbar">
      <strong>可用管理工具</strong>
      <NButton size="small" :loading="loading" :disabled="busy || loading" @click="detect">重新检测</NButton>
    </div>
    <NAlert v-if="detectionError" type="error" class="section-gap">{{ detectionError }}</NAlert>
    <NSpin :show="loading">
      <NEmpty v-if="!tools.length" :description="loading ? '正在检测本机工具…' : '未检测到管理工具，请在下方手动添加'" class="database-tool-empty" />
      <div v-for="tool in tools" :key="tool.id" class="database-tool-item">
        <div class="database-tool-heading">
          <NSpace align="center" :size="6">
            <strong>{{ tool.name }}</strong>
            <NTag size="small" :bordered="false">{{ tool.source }}</NTag>
            <NTag v-if="!tool.canOpen" size="small" type="warning" :bordered="false">待配置</NTag>
          </NSpace>
          <NSpace :size="6">
            <NButton v-if="tool.custom" size="small" quaternary :disabled="busy || loading" @click="removeTool(tool)">移除</NButton>
            <NButton v-if="tool.canOpen" size="small" type="primary" secondary :disabled="busy || loading" @click="openTool(tool)">打开</NButton>
            <NButton v-else-if="tool.kind === 'phpmyadmin'" size="small" :disabled="busy" @click="configureAddress">设置地址</NButton>
          </NSpace>
        </div>
        <p class="mono database-tool-path">{{ tool.url || tool.path }}</p>
        <p v-if="tool.note" class="muted">{{ tool.note }}</p>
      </div>
    </NSpin>
    <p class="footnote">检测已安装程序、PATH、常见安装目录和已配置站点。其他位置的便携版客户端可手动添加。</p>
    <NDivider />
    <div ref="manualSection"><strong>手动添加工具</strong>
    <NFormItem label="工具类型" class="database-tool-form">
      <NSelect :value="kind" :options="kindOptions" :disabled="busy" @update:value="changeKind" />
    </NFormItem>
    <NFormItem :label="kind === 'desktop' ? '客户端程序' : 'phpMyAdmin 访问地址'">
      <div class="input-action">
        <NInput ref="targetInput" v-model:value="target" :readonly="kind === 'desktop'" :disabled="busy"
          :placeholder="kind === 'desktop' ? '选择数据库客户端的 .exe 程序' : '例如：http://localhost/phpmyadmin/'" />
        <NButton v-if="kind === 'desktop'" :disabled="busy" @click="chooseClient">浏览</NButton>
        <NButton :disabled="busy || !target.trim()" :loading="busy" @click="addTool">添加</NButton>
      </div>
    </NFormItem>
    </div>
    <template #footer><NSpace justify="end"><NButton :disabled="busy" @click="emit('update:show', false)">关闭</NButton></NSpace></template>
  </NModal>
</template>
