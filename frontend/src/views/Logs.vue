<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from "vue";
import {
  NAlert,
  NButton,
  NCard,
  NEmpty,
  NIcon,
  NSelect,
  NSwitch,
} from "naive-ui";
import { RefreshOutline } from "@vicons/ionicons5";
import { api } from "../api";
import { instances, sites, state, uiActive } from "../state";
import { errorText } from "../state";
const kind = ref(state.logTarget?.kind || "instance");
const targetID = ref<string | null>(state.logTarget?.id ?? null);
const lines = ref(200);
const content = ref("");
const error = ref("");
const loading = ref(false);
const auto = ref(false);
const options = computed(() =>
  (kind.value === "instance" ? instances.value : sites.value).map((item) => ({
    label: item.name,
    value: item.id,
  })),
);
watch(kind, () => {
  targetID.value = null;
  content.value = "";
  error.value = "";
});
watch([targetID, lines], () => {
  void read();
});
async function read() {
  if (!uiActive.value || !targetID.value || loading.value) return;
  const id = targetID.value;
  const logKind = kind.value;
  loading.value = true;
  try {
    const value = await api.ReadLog(logKind, id, lines.value);
    if (targetID.value === id && kind.value === logKind) {
      content.value = value;
      error.value = "";
    }
  } catch (failure) {
    error.value = errorText(failure);
  } finally {
    loading.value = false;
  }
}
let timer: number | undefined;
watch(
  [auto, uiActive],
  ([enabled, active], [, wasActive]) => {
    window.clearInterval(timer);
    timer = undefined;
    if (!enabled || !active) return;
    if (!wasActive) void read();
    timer = window.setInterval(() => void read(), 2500);
  },
  { flush: "sync" },
);
onMounted(() => void read());
onUnmounted(() => window.clearInterval(timer));
</script>

<template>
  <div class="page-heading">
    <h1>运行日志</h1>
    <div class="heading-actions">
      <span class="muted">自动刷新</span
      ><NSwitch
        v-model:value="auto"
        size="small"
        :disabled="!targetID"
      /><NButton :loading="loading" :disabled="!targetID" @click="read"
        ><template #icon
          ><NIcon><RefreshOutline /></NIcon></template
        >刷新</NButton
      >
    </div>
  </div>
  <NCard :bordered="false" class="panel"
    ><div class="table-toolbar log-toolbar">
      <div class="filter-controls">
        <NSelect
          v-model:value="kind"
          :options="[
            { label: '服务实例', value: 'instance' },
            { label: '本地站点', value: 'site' },
          ]"
          style="width: 130px"
        /><NSelect
          v-model:value="targetID"
          :options="options"
          filterable
          placeholder="选择日志来源"
          style="width: 260px"
        />
      </div>
      <NSelect
        v-model:value="lines"
        :options="[
          { label: '最近 100 行', value: 100 },
          { label: '最近 200 行', value: 200 },
          { label: '最近 500 行', value: 500 },
          { label: '最近 1000 行', value: 1000 },
        ]"
        style="width: 145px"
      />
    </div>
    <NAlert v-if="error" type="error" class="section-gap">{{ error }}</NAlert
    ><NEmpty
      v-if="!targetID"
      description="选择服务实例或站点以查看日志"
      class="empty-block"
    />
    <div v-else class="log-viewer">
      <div class="log-viewer-bar">
        <span class="status-dot"></span
        >{{ options.find((option) => option.value === targetID)?.label
        }}<span class="log-viewer-hint">最后 {{ lines }} 行</span>
      </div>
      <pre v-if="content">{{ content }}</pre>
      <div v-else class="log-placeholder">
        {{
          loading
            ? "正在读取日志…"
            : error
              ? "日志读取失败，请查看错误信息"
              : "暂无日志内容"
        }}
      </div>
    </div></NCard
  >
</template>
