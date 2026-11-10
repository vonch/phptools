<script setup lang="ts">
import { computed, ref } from "vue";
import {
  NAlert,
  NButton,
  NCard,
  NEmpty,
  NIcon,
  NProgress,
  NSelect,
  NTag,
} from "naive-ui";
import {
  CheckmarkCircleOutline,
  BuildOutline,
  AlertCircleOutline,
  PulseOutline,
} from "@vicons/ionicons5";
import { api } from "../api";
import { useAction } from "../composables";
import { activeTasks, navigate, snapshot, state, viewLog } from "../state";
import { progressPercent, type HealthItem, type Page } from "../types";
import RuntimeDependencies from "../components/RuntimeDependencies.vue";

const { busy, run } = useAction();
const runtimeVisible = ref(false);
const runtimeRef = ref<{ check: () => Promise<void> } | null>(null);
const repairing = ref<string | null>(null);
const report = computed(() => snapshot.value?.healthReport);
const items = computed(() => report.value?.items ?? []);
const hasRoot = computed(() => !!snapshot.value?.settings.root);
const detectionTask = computed(() =>
  activeTasks.value.find((task) => task.action === "environment-check" || task.action === "environment-repair"),
);
const filter = ref("all");
const filterOptions = [
  { label: "全部结果", value: "all" },
  { label: "需处理", value: "attention" },
  { label: "已修复", value: "repaired" },
  { label: "通过", value: "ok" },
];
const filteredItems = computed(() =>
  items.value.filter((item) =>
    filter.value === "all"
      ? true
      : filter.value === "attention"
        ? item.status === "warning" || item.status === "error"
        : item.status === filter.value,
  ),
);
const showPassed = ref(false);
const passedItems = computed(() => filteredItems.value.filter((item) => item.status === "ok"));
const visibleItems = computed(() =>
  showPassed.value || filter.value === "ok"
    ? filteredItems.value
    : filteredItems.value.filter((item) => item.status !== "ok"),
);
const statusNames: Record<HealthItem["status"], string> = {
  ok: "通过",
  repaired: "已修复",
  warning: "需处理",
  error: "异常",
};
const statusTypes = {
  ok: "success",
  repaired: "info",
  warning: "warning",
  error: "error",
} as const;
const triggerNames = {
  startup: "应用启动",
  manual: "手动检测",
  operation: "操作异常后自动检测",
};
const summary = computed(() => [
  {
    title: "通过",
    value: items.value.filter((item) => item.status === "ok").length,
    icon: CheckmarkCircleOutline,
    tone: "green",
    hint: "检测正常，无需处理",
  },
  {
    title: "已修复",
    value: items.value.filter((item) => item.status === "repaired").length,
    icon: BuildOutline,
    tone: "blue",
    hint: "已完成安全修复",
  },
  {
    title: "需处理",
    value: items.value.filter(
      (item) => item.status === "warning" || item.status === "error",
    ).length,
    icon: AlertCircleOutline,
    tone: "purple",
    hint: "请查看原因和处理建议",
  },
]);
function checkEnvironment() {
  void run(() => api.CheckEnvironment());
}
function checkRuntime() {
  void runtimeRef.value?.check();
}
function cancelDetection() {
  const task = detectionTask.value;
  if (task) void run(() => api.CancelTask(task.id));
}
function repair(item: HealthItem) {
  if (!item.repairable || busy.value || repairing.value || detectionTask.value) return;
  repairing.value = item.repairId || item.id;
  void run(() => api.RepairHealthItem(item.repairId || item.id)).finally(() => {
    repairing.value = null;
  });
}
function needsAttention(item: HealthItem) {
  return item.status === "warning" || item.status === "error";
}
function treatmentEntry(item: HealthItem): { page: Page; label: string } {
  if (item.id === "environment:hosts") return { page: "sites", label: "核对站点域名" };
  if (item.id.startsWith("site:")) {
    if (item.id.endsWith(":nginx")) return { page: "packages", label: "安装 Nginx 组件" };
    if (item.id.endsWith(":php")) return { page: "instances", label: "检查 PHP 实例" };
    return { page: "sites", label: "修改站点设置" };
  }
  if (item.id.endsWith(":package")) return { page: "packages", label: "检查组件安装" };
  if (item.id.endsWith(":root")) return { page: "settings", label: "核对环境目录" };
  return { page: "instances", label: "检查服务实例" };
}
function canViewLog(item: HealthItem) {
  return snapshot.value?.instances.some((instance) => instance.id === item.targetId) ?? false;
}
function formatTime(value: string) {
  return new Date(value).toLocaleString("zh-CN", { hour12: false });
}
</script>

<template>
  <div class="page-heading">
    <h1>检测</h1>
    <div class="heading-actions">
      <NButton v-if="!runtimeVisible" @click="checkRuntime">检测运行依赖</NButton>
      <NButton
        type="primary"
        :loading="busy || !!detectionTask"
        :disabled="!hasRoot || busy || !!detectionTask"
        @click="checkEnvironment"
      >
        <template #icon><NIcon><PulseOutline /></NIcon></template>
        检测环境
      </NButton>
    </div>
  </div>
  <RuntimeDependencies ref="runtimeRef" @visible="runtimeVisible = $event" />
  <NAlert v-if="!hasRoot" type="info" class="section-gap">
    请先完成环境目录初始化，再进行检测。
  </NAlert>
  <NAlert v-if="runtimeVisible" type="info" class="section-gap" title="检测与修复">
    应用启动和服务操作失败后会进行轻量检测，并自动修复已核实的过期状态与缺失运行目录。
    手动检测会列出异常；可修复项提供修复按钮，其他问题提供具体处理建议和操作入口。
  </NAlert>
  <NCard v-if="detectionTask" :bordered="false" class="panel section-gap">
    <div class="table-toolbar">
      <div>
        <strong>{{ detectionTask.action === 'environment-repair' ? '正在修复检测项' : '正在检测环境' }}</strong>
        <p class="muted">{{ detectionTask.message || "正在检查运行状态…" }}</p>
      </div>
      <div class="heading-actions">
        <NButton size="small" :disabled="busy" @click="cancelDetection">取消任务</NButton>
        <NButton size="small" secondary @click="state.taskPanel = true">查看后台任务</NButton>
      </div>
    </div>
    <NProgress :percentage="progressPercent(detectionTask)" :show-indicator="false" :height="5" />
  </NCard>
  <div class="stats-grid">
    <NCard v-for="stat in summary" :key="stat.title" :bordered="false" class="stat-card">
      <div class="row-between">
        <span class="muted">{{ stat.title }}</span>
        <div class="stat-icon" :class="stat.tone"><NIcon :size="19"><component :is="stat.icon" /></NIcon></div>
      </div>
      <div class="stat-number">{{ report ? stat.value : "—" }}<span>项</span></div>
      <div class="stat-hint">{{ stat.hint }}</div>
    </NCard>
  </div>
  <NCard title="检测结果" :bordered="false" class="panel">
    <template #header-extra>
      <NTag v-if="report" :bordered="false" size="small">{{ report.full ? "完整检测" : "轻量检测" }}</NTag>
    </template>
    <template v-if="report">
      <div class="table-toolbar">
        <div class="health-report-meta">
          <span>触发来源：{{ triggerNames[report.trigger] }}</span>
          <span>开始：{{ formatTime(report.startedAt) }}</span>
          <span>完成：{{ report.completedAt ? formatTime(report.completedAt) : "检测中" }}</span>
        </div>
        <NSelect v-model:value="filter" :options="filterOptions" class="health-filter" size="small" />
      </div>
      <NAlert v-if="!report.full" type="info" :show-icon="false" class="section-gap">
        本次为轻量检测。点击“检测环境”可进一步检查配置语法、端口与服务响应。
      </NAlert>
      <p v-if="detectionTask" class="muted">以下为上一次检测结果，本次检测完成后将更新。</p>
      <NEmpty v-if="!filteredItems.length" :description="items.length ? '没有符合筛选条件的检测项' : '本次没有可检测的项目'" class="compact-empty" />
      <div v-if="passedItems.length && filter !== 'ok'" class="health-passed">
        <span>通过 {{ passedItems.length }} 项</span>
        <NButton size="small" quaternary @click="showPassed = !showPassed">{{ showPassed ? "收起" : "展开" }}</NButton>
      </div>
      <div v-for="item in visibleItems" :key="item.id" class="health-item">
        <div class="row-between">
          <div class="health-item-title">
            <strong>{{ item.check }}</strong>
            <span class="muted">{{ item.targetName }}</span>
          </div>
          <NTag :type="statusTypes[item.status]" size="small" :bordered="false">{{ statusNames[item.status] }}</NTag>
        </div>
        <p class="health-item-message">{{ item.message }}</p>
        <p v-if="item.suggestion" class="health-item-suggestion"><strong>处理建议：</strong>{{ item.suggestion }}</p>
        <div v-if="needsAttention(item)" class="heading-actions">
          <NButton
            v-if="item.repairable"
            size="small" type="primary" secondary
            :loading="repairing === (item.repairId || item.id) || detectionTask?.targetId === item.id"
            :disabled="busy || !!repairing || !!detectionTask"
            @click="repair(item)"
          >修复</NButton>
          <NButton v-if="item.id.endsWith(':cancelled')" size="small" :disabled="busy || !!detectionTask" @click="checkEnvironment">重新检测</NButton>
          <NButton v-else size="small" @click="navigate(treatmentEntry(item).page)">{{ treatmentEntry(item).label }}</NButton>
          <NButton v-if="canViewLog(item)" size="small" quaternary @click="viewLog('instance', item.targetId)">查看实例日志</NButton>
        </div>
      </div>
    </template>
    <NEmpty v-else :description="detectionTask ? '正在生成检测报告…' : '暂无检测报告，点击“检测环境”开始检查'" class="empty-block" />
  </NCard>
</template>
