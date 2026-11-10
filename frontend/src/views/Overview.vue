<script setup lang="ts">
import { computed, type Component } from "vue";
import {
  NAlert,
  NButton,
  NCard,
  NEmpty,
  NIcon,
  NProgress,
  NTag,
  NTooltip,
} from "naive-ui";
import {
  ArrowForwardOutline,
  PlayOutline,
  StopOutline,
  AddOutline,
  ServerOutline,
  GlobeOutline,
  CubeOutline,
  PulseOutline,
  HardwareChipOutline,
  InformationCircleOutline,
} from "@vicons/ionicons5";
import { api } from "../api";
import {
  activeTasks,
  instances,
  navigate,
  packages,
  running,
  sites,
  snapshot,
  state,
} from "../state";
import { compareServiceInstances } from "../serviceOrder";
import { formatBytes, kindName, progressPercent, stateName, type Instance } from "../types";
import { useAction } from "../composables";
const { busy, run } = useAction();
const installed = computed(() => packages.value.filter((pkg) => pkg.path));
const orderedInstances = computed(() =>
  instances.value
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
const memory = computed(() => snapshot.value?.memoryUsage);
const memorySize = computed(() => memory.value && !memory.value.error
  ? formatBytes(memory.value.workingSetBytes).split(" ") : ["—", ""]);
const memoryDetails = computed(() => {
  const usage = memory.value;
  if (!usage) return "正在采集内存数据。";
  if (usage.error) return usage.error;
  return [
    `主程序：${formatBytes(usage.managerBytes)}`,
    `WebView2 界面：${formatBytes(usage.webViewBytes)}`,
    `进程数：${usage.processCount}`,
    `专用提交内存：${formatBytes(usage.privateBytes)}`,
    "主数字为 Windows 工作集合计，含共享页面，可能重复统计；与任务管理器的默认内存列不完全相同。",
    "不含 PHP、MySQL、Nginx、Redis 或外部管理工具。随界面刷新采样，最短间隔 5 秒。",
    ...(usage.partial ? ["部分界面进程无法读取，当前数值仅含已读取的进程。"] : []),
  ].join("\n");
});
function startAll() {
  void run(() => api.StartAll());
}
function stopAll() {
  void run(() => api.StopAll());
}
function toggle(instance: Instance) {
  void run(() =>
    instance.state === "running"
      ? api.StopInstance(instance.id)
      : api.StartInstance(instance.id),
  );
}
interface OverviewStat {
  title: string;
  value: number | string;
  suffix: string;
  icon: Component;
  tone: string;
  hint: string;
  tooltip?: string;
}
const stats = computed<OverviewStat[]>(() => [
  {
    title: "运行中的服务",
    value: running.value.length,
    suffix: `/ ${instances.value.length}`,
    icon: PulseOutline,
    tone: "green",
    hint: "独立进程 · 本机访问",
  },
  {
    title: "已安装组件",
    value: installed.value.length,
    suffix: "个版本",
    icon: CubeOutline,
    tone: "blue",
    hint: "PHP / MySQL / Nginx / Redis",
  },
  {
    title: "本地站点",
    value: sites.value.length,
    suffix: "个站点",
    icon: GlobeOutline,
    tone: "purple",
    hint: `${sites.value.filter((site) => site.enabled).length} 个已启用`,
  },
  {
    title: "软件内存",
    value: `${memory.value?.partial ? "≥" : ""}${memorySize.value[0]}`,
    suffix: memorySize.value[1] || "",
    icon: HardwareChipOutline,
    tone: "amber",
    hint: memory.value?.error ? "暂时无法读取" : memory.value?.partial
      ? "部分进程未读取 · 查看详情" : "工作集 · 含界面，不含服务",
    tooltip: memoryDetails.value,
  },
]);
</script>

<template>
  <div class="page-heading">
    <h1>环境概览</h1>
    <div class="heading-actions">
      <NButton :disabled="!running.length || busy" @click="stopAll"
        ><template #icon
          ><NIcon><StopOutline /></NIcon></template
        >全部停止</NButton
      ><NButton
        type="primary"
        :disabled="!instances.length || busy"
        :loading="busy"
        @click="startAll"
        ><template #icon
          ><NIcon><PlayOutline /></NIcon></template
        >全部启动</NButton
      >
    </div>
  </div>
  <div class="stats-grid">
    <NCard
      v-for="stat in stats"
      :key="stat.title"
      :bordered="false"
      class="stat-card"
      ><div class="row-between">
        <span class="muted">{{ stat.title }}
          <NTooltip v-if="stat.tooltip">
            <template #trigger><NIcon :size="14" class="memory-help"><InformationCircleOutline /></NIcon></template>
            <div class="memory-tooltip">{{ stat.tooltip }}</div>
          </NTooltip>
        </span>
        <div class="stat-icon" :class="stat.tone">
          <NIcon :size="19"><component :is="stat.icon" /></NIcon>
        </div>
      </div>
      <div class="stat-number">
        {{ stat.value }}<span>{{ stat.suffix }}</span>
      </div>
      <div class="stat-hint">{{ stat.hint }}</div></NCard
    >
  </div>
  <div v-if="snapshot?.diagnostics.length" class="section-gap">
    <NAlert
      v-for="(item, index) in snapshot.diagnostics"
      :key="index"
      :type="
        item.level === 'error'
          ? 'error'
          : item.level === 'warning'
            ? 'warning'
            : 'info'
      "
      class="diagnostic"
      >{{ item.message }}</NAlert
    >
  </div>
  <div class="overview-grid">
    <NCard title="服务状态" :bordered="false" class="panel"
      ><template #header-extra
        ><NButton text type="primary" @click="navigate('instances')"
          >管理实例<NIcon class="inline-icon"
            ><ArrowForwardOutline /></NIcon></NButton
      ></template>
      <div v-if="!instances.length" class="welcome-empty">
        <div class="empty-icon">
          <NIcon :size="28"><ServerOutline /></NIcon>
        </div>
        <h3>从你的第一个服务开始</h3>
        <p>安装组件后，创建独立实例。每个实例都有自己的配置和端口。</p>
        <NButton
          type="primary"
          @click="navigate(installed.length ? 'instances' : 'packages')"
          ><template #icon
            ><NIcon><AddOutline /></NIcon></template
          >{{ installed.length ? "创建实例" : "前往组件中心" }}</NButton
        >
      </div>
      <div
        v-for="instance in orderedInstances"
        :key="instance.id"
        class="service-row"
      >
        <div class="component-avatar" :class="instance.kind">
          {{ { php: "P", mysql: "M", nginx: "N", redis: "R" }[instance.kind] }}
        </div>
        <div class="service-description">
          <strong>{{ instance.name }}</strong
          ><span
            >{{ kindName[instance.kind] }}
            <span class="separator">·</span> 127.0.0.1:{{ instance.port }}</span
          >
        </div>
        <NTag
          size="small"
          :bordered="false"
          :type="
            instance.state === 'running'
              ? 'success'
              : instance.state === 'error'
                ? 'error'
                : 'default'
          "
          >{{ stateName[instance.state] || instance.state }}</NTag
        ><NButton
          quaternary
          circle
          size="small"
          :disabled="busy"
          :title="instance.state === 'running' ? '停止' : '启动'"
          @click="toggle(instance)"
          ><template #icon
            ><NIcon
              ><StopOutline v-if="instance.state === 'running'" /><PlayOutline
                v-else /></NIcon></template
        ></NButton>
      </div>
    </NCard>
    <NCard title="快速开始" :bordered="false" class="panel quick-start"
      ><div class="step" @click="navigate('packages')">
        <div class="step-number" :class="{ complete: installed.length }">
          01
        </div>
        <div>
          <strong>安装所需组件</strong>
          <p>从经过校验的 Windows 版本中选择</p>
        </div>
        <NIcon><ArrowForwardOutline /></NIcon>
      </div>
      <div class="step" @click="navigate('instances')">
        <div class="step-number" :class="{ complete: instances.length }">
          02
        </div>
        <div>
          <strong>创建服务实例</strong>
          <p>为不同项目设置独立版本和端口</p>
        </div>
        <NIcon><ArrowForwardOutline /></NIcon>
      </div>
      <div class="step" @click="navigate('sites')">
        <div class="step-number" :class="{ complete: sites.length }">03</div>
        <div>
          <strong>添加本地站点</strong>
          <p>绑定域名，选择项目与 PHP 实例</p>
        </div>
        <NIcon><ArrowForwardOutline /></NIcon>
      </div>
      <div class="local-note">
        <i class="status-dot"></i>数据库默认只监听本机地址
      </div></NCard
    >
  </div>
  <NCard title="最近的后台任务" :bordered="false" class="panel section-gap"
    ><template #header-extra
      ><NButton text type="primary" @click="state.taskPanel = true"
        >查看全部<NIcon class="inline-icon"
          ><ArrowForwardOutline /></NIcon></NButton></template
    ><NEmpty
      v-if="!activeTasks.length"
      description="当前没有正在执行的任务"
      size="small"
      class="compact-empty" />
    <div
      v-for="task in activeTasks.slice(0, 3)"
      :key="task.id"
      class="overview-task"
    >
      <div class="row-between">
        <strong>{{ task.message || task.action }}</strong
        ><span class="muted">{{ Math.round(progressPercent(task)) }}%</span>
      </div>
      <NProgress
        :percentage="progressPercent(task)"
        :show-indicator="false"
        :height="5"
      /></div
  ></NCard>
</template>
