<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from "vue";
import {
  NAlert,
  NBadge,
  NButton,
  NDrawer,
  NDrawerContent,
  NEmpty,
  NIcon,
  NInput,
  NMenu,
  NModal,
  NProgress,
  NSpin,
  NTag,
  useDialog,
} from "naive-ui";
import {
  RefreshOutline,
  FolderOpenOutline,
  TerminalOutline,
} from "@vicons/ionicons5";
import { api } from "./api";
import {
  activeTasks,
  refresh,
  snapshot,
  startUpdates,
  state,
  tasks,
} from "./state";
import {
  formatBytes,
  isActiveTask,
  progressPercent,
  stateName,
  type Page,
} from "./types";
import { useAction } from "./composables";
import { runtimeDownloadOffer } from "./runtimePrompt";
import { defaultSidebar, pageInfo, sidebarLabel } from "./sidebar";
import Overview from "./views/Overview.vue";
import Packages from "./views/Packages.vue";
import Instances from "./views/Instances.vue";
import Databases from "./views/Databases.vue";
import Sites from "./views/Sites.vue";
import Logs from "./views/Logs.vue";
import Settings from "./views/Settings.vue";
import Health from "./views/Health.vue";
import { startAppUpdateChecks, updateAvailable, updateInfo } from "./updateState";

const { busy, run, message } = useAction();
const dialog = useDialog();
let runtimePromptOpen = false;
function offerRuntimeDownload(error: string) {
  const id = runtimeDownloadOffer(error);
  if (!id || runtimePromptOpen) return false;
  runtimePromptOpen = true;
  dialog.warning({
    title: "缺少运行库",
    content: "PHP 无法启动，系统缺少 Visual C++ v14 x64。是否下载微软官方安装程序？安装完成后，回到服务实例重新启动。",
    positiveText: "下载",
    negativeText: "稍后",
    onPositiveClick: () => run(() => api.OpenRuntimeDownload(id)),
    onAfterLeave: () => {
      runtimePromptOpen = false;
    },
  });
  return true;
}
const sidebarItems = computed(() => snapshot.value?.sidebar ?? defaultSidebar());
const pages = computed(() =>
  sidebarItems.value
    .filter((item) => !item.hidden)
    .map((item) => ({ ...pageInfo(item.key), label: sidebarLabel(item) })),
);
const pageTitle = computed(() => {
  const item = sidebarItems.value.find((entry) => entry.key === state.page);
  return item ? sidebarLabel(item) : pageInfo(state.page).label;
});
const rootInput = ref("D:\\PHPTools");
const needsSetup = computed(
  () => !!snapshot.value && !snapshot.value.settings.root,
);
const actionNames: Record<string, string> = {
  install: "安装组件",
  refresh: "刷新版本目录",
  start: "启动实例",
  stop: "停止实例",
  restart: "重启实例",
  "start-all": "启动全部",
  "start-php": "启动已安装的 PHP",
  "resume-instances": "恢复升级前的实例",
  "stop-all": "停止全部",
  "database-import": "导入数据库",
  "database-export": "导出数据库",
  "environment-check": "检测",
  "environment-repair": "修复检测项",
  "app-update": "应用升级",
};
const seenTasks = new Map<string, string>();
let tasksInitialized = false;
watch(tasks, (value) => {
  for (const task of value) {
    const previous = seenTasks.get(task.id);
    if (
      tasksInitialized &&
      previous !== task.state &&
      task.state === "failed"
    ) {
      if (!offerRuntimeDownload(task.error || "")) {
        message.error(task.error || "后台任务失败", { duration: 10000 });
      }
      state.taskPanel = true;
    }
    if (previous && previous !== task.state && task.state === "completed")
      message.success(task.action === "app-update"
        ? "更新包已准备，正在重新启动应用"
        : `${actionNames[task.action] || task.action}已完成`);
    seenTasks.set(task.id, task.state);
  }
  tasksInitialized = true;
});
async function chooseRoot() {
  const path = await run(() =>
    api.SelectDirectory("选择 PHPTools 独立环境目录"),
  );
  if (path) rootInput.value = path;
}
function selectPage(value: string) {
  state.page = value as Page;
}
function cancelTask(id: string) {
  void run(() => api.CancelTask(id));
}
function setRoot() {
  void run(async () => {
    await api.SetRoot(rootInput.value.trim());
    await api.StartInstalledPHP();
  }, "环境目录已就绪");
}
let stopUpdates: (() => void) | undefined;
let stopAppUpdateChecks: (() => void) | undefined;
onMounted(() => {
  stopUpdates = startUpdates();
  stopAppUpdateChecks = startAppUpdateChecks();
});
onUnmounted(() => {
  stopUpdates?.();
  stopAppUpdateChecks?.();
});
</script>

<template>
  <div class="app-shell">
    <aside class="sidebar">
      <div class="brand">
        <div class="brand-symbol">
          <NIcon :size="23"><TerminalOutline /></NIcon>
        </div>
        <div><strong>PHPTools</strong><span>本地开发环境</span></div>
      </div>
      <div class="sidebar-label">工作空间</div>
      <NMenu
        :value="state.page"
        :options="pages"
        :indent="20"
        :theme-overrides="{
          itemTextColor: '#aab7c6',
          itemTextColorHover: '#ffffff',
          itemTextColorActive: '#ffffff',
          itemTextColorActiveHover: '#ffffff',
          itemIconColor: '#8fa0b3',
          itemIconColorHover: '#ffffff',
          itemIconColorActive: '#61d5b7',
          itemIconColorActiveHover: '#61d5b7',
          itemColorHover: '#253447',
          itemColorActive: '#253c47',
          itemColorActiveHover: '#253c47',
          borderRadius: '7px',
        }"
        @update:value="selectPage"
      />
      <div class="sidebar-footer">
        <span class="environment-dot"></span>
        <div><strong>Windows x64</strong><span>本机独立环境</span></div>
        <small class="sidebar-version">
          <button
            v-if="updateAvailable"
            type="button"
            class="update-mark"
            :title="`发现新版本 v${updateInfo?.latestVersion}`"
            @click="selectPage('settings')"
          >新</button>
          v{{ snapshot?.settings.version || "—" }}
        </small>
      </div>
    </aside>
    <div class="workspace">
      <header class="topbar">
        <div class="breadcrumbs">
          工作空间 <span>/</span> <strong>{{ pageTitle }}</strong>
        </div>
        <div class="topbar-actions">
          <NButton
            quaternary
            size="small"
            :loading="state.loading"
            title="刷新状态"
            @click="refresh"
            ><template #icon
              ><NIcon><RefreshOutline /></NIcon></template></NButton
          ><NBadge
            :value="activeTasks.length"
            :show="activeTasks.length > 0"
            type="success"
            ><NButton size="small" secondary @click="state.taskPanel = true"
              >后台任务</NButton
            ></NBadge
          >
        </div>
      </header>
      <main class="page-content">
        <NAlert
          v-if="state.connectionError"
          type="error"
          title="无法连接桌面服务"
          class="section-gap"
          >{{ state.connectionError }}
          <NButton size="small" @click="refresh">重试</NButton></NAlert
        >
        <NSpin
          v-if="state.loading"
          class="initial-loading"
          description="正在读取环境状态…"
        />
        <template v-else-if="snapshot">
          <Overview v-if="state.page === 'overview'" />
          <Packages v-else-if="state.page === 'packages'" />
          <Instances v-else-if="state.page === 'instances'" />
          <Databases v-else-if="state.page === 'databases'" />
          <Sites v-else-if="state.page === 'sites'" />
          <Health v-else-if="state.page === 'health'" />
          <Logs v-else-if="state.page === 'logs'" />
          <Settings v-else-if="state.page === 'settings'" />
        </template>
        <NEmpty
          v-else-if="!state.loading"
          description="请在 PHPTools 桌面应用中打开此界面"
          class="initial-loading"
        />
      </main>
      <footer class="statusbar">
        <span
          ><i class="status-dot" :class="{ offline: state.connectionError }"></i
          >{{
            state.connectionError
              ? "桌面服务未连接"
              : "仅管理 PHPTools 创建的服务"
          }}</span
        ><span>{{ snapshot?.settings.root || "尚未设置环境目录" }}</span>
      </footer>
    </div>
    <NDrawer v-model:show="state.taskPanel" :width="430"
      ><NDrawerContent title="后台任务" closable>
        <NEmpty
          v-if="!tasks.length"
          description="暂无任务"
          class="empty-block"
        />
        <div v-for="task in tasks" :key="task.id" class="task-card">
          <div class="row-between">
            <strong>{{ actionNames[task.action] || task.action }}</strong
            ><NTag
              :type="
                task.state === 'failed'
                  ? 'error'
                  : isActiveTask(task)
                    ? 'info'
                    : 'default'
              "
              size="small"
              :bordered="false"
              >{{ stateName[task.state] || task.state }}</NTag
            >
          </div>
          <p class="task-target">{{ task.targetId }}</p>
          <NProgress
            v-if="isActiveTask(task) || task.bytesTotal"
            :percentage="progressPercent(task)"
            :show-indicator="false"
            :status="task.state === 'failed' ? 'error' : 'default'"
            :height="5"
          />
          <p class="muted">
            {{ task.message }}
            <span v-if="task.bytesTotal"
              >{{ formatBytes(task.bytesDone) }} /
              {{ formatBytes(task.bytesTotal) }}</span
            >
          </p>
          <NAlert v-if="task.error" type="error" :show-icon="false">{{
            task.error
          }}</NAlert>
          <NButton
            v-if="isActiveTask(task)"
            size="tiny"
            class="task-cancel"
            @click="cancelTask(task.id)"
            >取消任务</NButton
          >
        </div>
      </NDrawerContent></NDrawer
    >
    <NModal
      :show="needsSetup"
      preset="card"
      title="为开发环境选择一个家"
      :closable="false"
      :mask-closable="false"
      :close-on-esc="false"
      class="setup-modal"
    >
      <div class="setup-hero">
        <NIcon :size="34" color="#187868"><FolderOpenOutline /></NIcon>
        <p>
          组件、配置和数据库将保存在这个独立目录。<br />请使用一个新的空目录。
        </p>
      </div>
      <label class="field-label">环境根目录</label>
      <div class="input-action">
        <NInput v-model:value="rootInput" placeholder="D:\PHPTools" /><NButton
          @click="chooseRoot"
          >浏览</NButton
        >
      </div>
      <p class="muted">
        建议：{{
          snapshot?.settings.suggestedRoot || "D:\\PHPTools"
        }}。设置后即可在线安装组件。
      </p>
      <template #footer
        ><NButton
          type="primary"
          block
          :loading="busy"
          :disabled="!rootInput.trim()"
          @click="setRoot"
          >创建独立环境</NButton
        ></template
      >
    </NModal>
  </div>
</template>
