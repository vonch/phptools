<script setup lang="ts">
import { computed, ref } from "vue";
import {
  NAlert,
  NButton,
  NCard,
  NEmpty,
  NIcon,
  NInput,
  NProgress,
  NSelect,
  NTabPane,
  NTabs,
  NTag,
  NTooltip,
} from "naive-ui";
import {
  DownloadOutline,
  SearchOutline,
  SyncOutline,
  TrashOutline,
  AddOutline,
} from "@vicons/ionicons5";
import { api } from "../api";
import { activeTasks, instances, navigate, packages, state } from "../state";
import { useAction } from "../composables";
import { kindName, progressPercent, type Kind, type Package } from "../types";
const { busy, run, confirm } = useAction();
const tab = ref("php");
const search = ref("");
const filter = ref("all");
const visible = computed(() =>
  packages.value
    .filter(
      (pkg) =>
        pkg.kind === tab.value &&
        `${pkg.version} ${pkg.channel} ${pkg.source}`
          .toLowerCase()
          .includes(search.value.toLowerCase()) &&
        (filter.value === "all" ||
          (filter.value === "installed" ? !!pkg.path : !pkg.path)),
    )
    .sort((a, b) =>
      b.version.localeCompare(a.version, undefined, { numeric: true }),
    ),
);
const taskFor = (id: string) =>
  activeTasks.value.find((task) => task.targetId === id);
const referenced = (id: string) =>
  instances.value.some((instance) => instance.packageId === id);
function uninstall(pkg: Package) {
  confirm(
    `卸载 ${kindName[pkg.kind]} ${pkg.version}`,
    "将删除此版本的程序文件。有关联实例的组件不能卸载。",
    () => api.UninstallPackage(pkg.id),
  );
}
async function install(pkg: Package) {
  await run(() => api.InstallPackage(pkg.id));
  state.taskPanel = true;
}
async function refreshCatalog() {
  await run(() => api.RefreshCatalog());
  state.taskPanel = true;
}
function cancel(id: string) {
  const task = taskFor(id);
  if (task) void run(() => api.CancelTask(task.id));
}
const descriptions: Record<Kind, string> = {
  php: "Windows x64 · NTS · 不同版本可同时运行",
  mysql: "独立数据目录 · 多实例并行 · 密码存入 Windows 凭据管理器",
  nginx: "官方 Windows 发行版 · 支持稳定版与主线版",
  redis: "redis-windows 第三方发行版 · Cygwin 普通 ZIP",
};
</script>

<template>
  <div class="page-heading">
    <h1>组件中心</h1>
    <NButton :loading="busy" @click="refreshCatalog"
      ><template #icon
        ><NIcon><SyncOutline /></NIcon></template
      >刷新版本目录</NButton
    >
  </div>
  <NCard :bordered="false" class="panel component-panel"
    ><NTabs v-model:value="tab" type="line" animated
      ><NTabPane v-for="(label, kind) in kindName" :key="kind" :name="kind"
        ><template #tab>{{ label }}</template></NTabPane
      ></NTabs
    >
    <div class="table-toolbar">
      <p class="muted">{{ descriptions[tab as Kind] }}</p>
      <div class="filter-controls">
        <NInput
          v-model:value="search"
          placeholder="搜索版本"
          clearable
          style="width: 180px"
          ><template #prefix
            ><NIcon><SearchOutline /></NIcon></template></NInput
        ><NSelect
          v-model:value="filter"
          :options="[
            { label: '全部版本', value: 'all' },
            { label: '已安装', value: 'installed' },
            { label: '未安装', value: 'available' },
          ]"
          style="width: 125px"
        />
      </div>
    </div>
    <NAlert
      v-if="tab === 'redis'"
      type="info"
      :show-icon="false"
      class="section-gap"
      >Redis 使用第三方 redis-windows 发行版，安装包包含 Cygwin 运行库。</NAlert
    >
    <NEmpty
      v-if="!visible.length"
      :description="
        packages.length
          ? '没有符合条件的版本'
          : '版本目录暂不可用，请刷新后查看后台任务详情'
      "
      class="empty-block"
    />
    <div v-for="pkg in visible" :key="pkg.id" class="package-row">
      <div class="component-avatar" :class="pkg.kind">
        {{ { php: "P", mysql: "M", nginx: "N", redis: "R" }[pkg.kind] }}
      </div>
      <div class="package-info">
        <div class="package-title">
          <strong>{{ kindName[pkg.kind] }} {{ pkg.version }}</strong
          ><NTag v-if="pkg.channel" size="small" :bordered="false">{{
            pkg.channel
          }}</NTag
          ><NTag v-if="pkg.eol" type="warning" size="small" :bordered="false"
            >已停止维护</NTag
          >
        </div>
        <div class="package-meta">
          {{ pkg.arch }} <span>·</span> {{ pkg.source }} <span>·</span
          ><span :class="pkg.verified ? 'verified' : 'muted'">{{
            pkg.verified ? "来源校验已配置" : "暂未通过校验"
          }}</span>
        </div>
        <div v-if="taskFor(pkg.id)" class="package-progress">
          <NProgress
            :percentage="progressPercent(taskFor(pkg.id)!)"
            :show-indicator="false"
            :height="4"
          /><span>{{ taskFor(pkg.id)?.message || "正在处理…" }}</span>
        </div>
      </div>
      <div class="package-actions">
        <template v-if="pkg.path"
          ><NTag type="success" size="small" :bordered="false">已安装</NTag
          ><NButton size="small" @click="navigate('instances')"
            ><template #icon
              ><NIcon><AddOutline /></NIcon></template
            >创建实例</NButton
          ><NTooltip
            ><template #trigger
              ><span
                ><NButton
                  quaternary
                  circle
                  size="small"
                  :disabled="referenced(pkg.id) || busy"
                  @click="uninstall(pkg)"
                  ><template #icon
                    ><NIcon
                      ><TrashOutline /></NIcon></template></NButton></span></template
            >{{
              referenced(pkg.id) ? "有实例正在使用，无法卸载" : "卸载此版本"
            }}</NTooltip
          ></template
        ><NButton
          v-else-if="taskFor(pkg.id)"
          size="small"
          @click="cancel(pkg.id)"
          >取消</NButton
        ><NButton
          v-else
          type="primary"
          secondary
          size="small"
          :disabled="!pkg.verified || busy"
          @click="install(pkg)"
          ><template #icon
            ><NIcon><DownloadOutline /></NIcon></template
          >安装</NButton
        >
      </div>
    </div>
  </NCard>
  <p class="footnote">
    仅提供具备完整性校验信息的安装包。历史 PHP
    分支用于兼容旧项目，建议新项目选择仍在维护的版本。
  </p>
</template>
