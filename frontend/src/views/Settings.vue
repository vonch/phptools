<script setup lang="ts">
import { computed, onMounted, ref, watch } from "vue";
import {
  NAlert,
  NButton,
  NCard,
  NDescriptions,
  NDescriptionsItem,
  NIcon,
  NSpace,
  NSpin,
  NSwitch,
  NTag,
} from "naive-ui";
import { FolderOpenOutline } from "@vicons/ionicons5";
import { api } from "../api";
import { useAction } from "../composables";
import { errorText, navigate, snapshot, uiActive } from "../state";
import type { AutoStartStatus } from "../types";
import SidebarManager from "../components/SidebarManager.vue";
import AppUpdates from "../components/AppUpdates.vue";
const { run, confirm, message } = useAction();
const showSidebarManager = ref(false);
const autoStartStatus = ref<AutoStartStatus | null>(null);
const autoStartLoading = ref(false);
const autoStartSaving = ref(false);
const autoStartError = ref("");
const autoStartBusy = computed(
  () => autoStartLoading.value || autoStartSaving.value,
);

async function loadAutoStart() {
  if (autoStartBusy.value) return;
  autoStartLoading.value = true;
  autoStartError.value = "";
  try {
    autoStartStatus.value = await api.GetAutoStart();
  } catch (error) {
    autoStartError.value = `读取开机启动状态失败：${errorText(error)}`;
    message.error(autoStartError.value, { duration: 7000 });
  } finally {
    autoStartLoading.value = false;
  }
}

async function setAutoStart(enabled: boolean) {
  if (
    autoStartBusy.value ||
    !autoStartStatus.value ||
    autoStartStatus.value.conflict ||
    enabled === autoStartStatus.value.enabled
  ) return;
  autoStartSaving.value = true;
  autoStartError.value = "";
  try {
    const status = await api.SetAutoStart(enabled);
    autoStartStatus.value = status;
    if (status.enabled === enabled && !status.conflict) {
      message.success(enabled ? "已开启开机启动" : "已关闭开机启动");
    } else {
      message.warning(status.conflict || "开机启动设置尚未生效，请刷新状态后重试。");
    }
  } catch (error) {
    autoStartError.value = `保存开机启动设置失败：${errorText(error)}`;
    message.error(autoStartError.value, { duration: 7000 });
  } finally {
    autoStartSaving.value = false;
  }
}

onMounted(() => void loadAutoStart());
watch(uiActive, (active, previous) => {
  if (active && !previous) void loadAutoStart();
});
function openDirectory() {
  const root = snapshot.value?.settings.root;
  if (root) void run(() => api.OpenDirectory(root));
}
function quit() {
  confirm(
    "退出 PHPTools",
    "退出前将依次停止 Nginx、PHP、MySQL 和 Redis。若数据库停止失败，管理器会保持打开以便重试。",
    () => api.Quit(),
  );
}
</script>

<template>
  <div class="page-heading">
    <h1>设置</h1>
    <NTag :bordered="false">首版 · Windows x64</NTag>
  </div>
  <AppUpdates />
  <NCard title="环境存储" :bordered="false" class="panel section-gap"
    ><div class="setting-row">
      <div>
        <strong>独立环境根目录</strong>
        <p class="muted mono setting-path">{{ snapshot?.settings.root }}</p>
        <p class="muted">程序、实例、数据库、下载缓存和日志分别保存。</p>
      </div>
      <NButton @click="openDirectory"
        ><template #icon
          ><NIcon><FolderOpenOutline /></NIcon></template
        >打开目录</NButton
      >
    </div>
    <NAlert type="info" :show-icon="false"
      >环境初始化后不支持直接修改根目录。移动现有数据库需要单独进行数据迁移。</NAlert
    ></NCard
  >
  <NCard title="界面设置" :bordered="false" class="panel section-gap">
    <div class="setting-row">
      <div>
        <strong>侧栏菜单</strong>
        <p class="muted">调整左侧菜单的顺序，显示或隐藏不常用的入口。</p>
      </div>
      <NButton @click="showSidebarManager = true">管理菜单</NButton>
    </div>
  </NCard>
  <NCard title="运行依赖" :bordered="false" class="panel section-gap">
    <div class="setting-row">
      <div>
        <strong>WebView2 与 Visual C++ 运行库</strong>
        <p class="muted">检测当前电脑的运行依赖，查看缺失项与微软官方安装指引。</p>
      </div>
      <NButton @click="navigate('health')">检查运行依赖</NButton>
    </div>
  </NCard>
  <NCard title="桌面行为" :bordered="false" class="panel section-gap"
    ><div class="setting-row">
      <div>
        <strong>关闭窗口</strong>
        <p class="muted">隐藏到系统托盘，服务继续运行。</p>
      </div>
      <NTag size="small" :bordered="false">托盘常驻</NTag>
    </div>
    <div class="setting-row">
      <div>
        <strong>开机启动</strong>
        <p class="muted">登录 Windows 后自动启动 PHPTools。</p>
        <p class="muted">已安装的 PHP 会跟着打开。MySQL、Nginx 和 Redis 仍需手动启动。</p>
      </div>
      <NSpace align="center">
        <NSwitch
          v-if="autoStartStatus"
          :value="autoStartStatus.enabled"
          :loading="autoStartBusy"
          :disabled="autoStartBusy || !!autoStartStatus.conflict"
          aria-label="开机启动"
          @update:value="setAutoStart"
        />
        <NSpin v-else-if="autoStartLoading" size="small" aria-label="正在读取开机启动状态" />
        <NTag v-else size="small" :bordered="false">状态未知</NTag>
        <NButton size="small" quaternary :disabled="autoStartBusy" @click="loadAutoStart">
          刷新状态
        </NButton>
      </NSpace>
    </div>
    <NAlert
      v-if="autoStartError"
      type="error"
      class="section-gap"
      role="alert"
    >
      <NSpace vertical align="start">
        <span>{{ autoStartError }}</span>
        <NButton size="small" :loading="autoStartLoading" :disabled="autoStartBusy" @click="loadAutoStart">
          重新读取
        </NButton>
      </NSpace>
    </NAlert>
    <NAlert
      v-if="autoStartStatus?.conflict"
      type="warning"
      class="section-gap"
      role="alert"
    >
      <NSpace vertical align="start">
        <span>{{ autoStartStatus.conflict }}</span>
        <NButton size="small" :loading="autoStartLoading" :disabled="autoStartBusy" @click="loadAutoStart">
          重新检查
        </NButton>
      </NSpace>
    </NAlert>
    <div class="setting-row">
      <div>
        <strong>删除实例</strong>
        <p class="muted">保留实例文件和数据库，避免误删项目数据。</p>
      </div>
      <NTag size="small" :bordered="false">保留数据</NTag>
    </div>
    <div class="setting-row last">
      <div>
        <strong>退出应用</strong>
        <p class="muted">先停止受管理的服务，再退出 PHPTools。</p>
      </div>
      <NButton type="error" secondary @click="quit">停止服务并退出</NButton>
    </div></NCard
  >
  <NCard title="关于 PHPTools" :bordered="false" class="panel"
    ><NDescriptions :column="2" label-placement="left"
      ><NDescriptionsItem label="应用版本">{{
        snapshot?.settings.version || "—"
      }}</NDescriptionsItem
      ><NDescriptionsItem label="运行平台">Windows x64</NDescriptionsItem
      ><NDescriptionsItem label="桌面框架"
        >Wails v3.0.0-beta.32</NDescriptionsItem
      ><NDescriptionsItem label="本地连接"
        >127.0.0.1</NDescriptionsItem
      ></NDescriptions
    >
    <p class="footnote">
      面向本地 PHP
      开发的独立环境管理器。版本目录来自组件官方来源及标明的第三方发行。
    </p></NCard
  >
  <SidebarManager v-model:show="showSidebarManager" />
</template>
