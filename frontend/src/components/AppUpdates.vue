<script setup lang="ts">
import { computed } from "vue";
import { NAlert, NButton, NCard, NProgress, NTag } from "naive-ui";
import { api } from "../api";
import { useAction } from "../composables";
import { errorText, snapshot } from "../state";
import { formatBytes, progressPercent } from "../types";
import {
  cancelAppUpdate, checkAppUpdate, installAppUpdate, latestUpdateTask,
  updateAvailable, updateBusy, updateCancelling,
  updateCheckError, updateChecking, updateInfo, updateInProgress,
  updateOperationError, updateStarting, updateTask,
} from "../updateState";

const { run, message } = useAction();
const lastFailure = computed(() => latestUpdateTask.value?.state === "failed" ? latestUpdateTask.value.error : "");
const currentVersion = computed(() => snapshot.value?.settings.version || updateInfo.value?.currentVersion || "");
const status = computed(() => {
  if (updateInProgress.value) return "升级中";
  if (updateChecking.value) return "正在检查";
  if (updateCheckError.value) return "检查失败";
  if (updateAvailable.value) return `发现 ${updateInfo.value!.latestVersion}`;
  return updateInfo.value?.checkedAt && updateInfo.value?.latestVersion ? "当前版本无需升级" : "尚未检查";
});

function install() {
  void run(installAppUpdate);
}
function cancel() {
  void run(cancelAppUpdate);
}
function formatTime(value: string) {
  return new Date(value).toLocaleString("zh-CN", { hour12: false });
}
function openSite() {
  void api.OpenOfficialSite().catch((error: unknown) => message.error(errorText(error), { duration: 7000 }));
}
function openFeedback() {
  void api.OpenFeedback().catch((error: unknown) => message.error(errorText(error), { duration: 7000 }));
}
</script>

<template>
  <NCard title="应用升级" :bordered="false" class="panel section-gap app-updates">
    <template #header-extra>
      <NTag :bordered="false" size="small" :type="updateCheckError ? 'error' : updateAvailable ? 'info' : 'default'">{{ status }}</NTag>
    </template>
    <div class="setting-row">
      <div>
        <strong>当前版本 v{{ currentVersion || "—" }}</strong>
        <p class="muted">每次显示窗口时检查更新。30 秒内反复显示或缩小，只检查一次。有新版本时，侧栏左下角显示“新”。</p>
      </div>
      <NButton :loading="updateChecking" :disabled="updateBusy" @click="checkAppUpdate">检查更新</NButton>
    </div>
    <NAlert v-if="updateCheckError" type="error" title="检查更新失败" class="section-gap">
      {{ updateCheckError }}
    </NAlert>
    <NAlert v-if="updateOperationError" type="error" class="section-gap">{{ updateOperationError }}</NAlert>
    <NAlert v-if="lastFailure && !updateInProgress" type="error" title="上次升级未完成" class="section-gap">{{ lastFailure }}。可检查原因后重试。</NAlert>
    <NAlert v-if="latestUpdateTask?.state === 'cancelled' && !updateInProgress" type="info" class="section-gap" :show-icon="false">升级已取消，当前版本继续使用。</NAlert>
    <div v-if="updateAvailable" class="update-release">
      <div class="update-release-heading">
        <strong>新版本 v{{ updateInfo?.latestVersion }}</strong>
        <NTag v-if="updateInfo?.mandatory" size="small" :bordered="false" type="warning">重要更新</NTag>
      </div>
      <p v-if="updateInfo?.releaseDate" class="muted">发布时间：{{ updateInfo.releaseDate }}</p>
      <p v-if="updateInfo?.size" class="muted">下载大小：{{ formatBytes(updateInfo.size) }}</p>
      <p v-if="updateInfo?.notes" class="update-release-notes">{{ updateInfo.notes }}</p>
      <NAlert type="info" :show-icon="false" class="section-gap">
        升级会下载并校验新版本，停止受管服务，然后重新打开 PHPTools。重新打开后，会启动升级前正在运行的实例。请先保存正在进行的开发工作。
      </NAlert>
      <NButton type="primary" :loading="updateStarting" :disabled="updateBusy" @click="install">立即升级</NButton>
    </div>
    <div v-if="updateInProgress" class="update-progress">
      <strong>{{ updateTask?.message || "正在准备升级…" }}</strong>
      <NProgress v-if="updateTask" :percentage="progressPercent(updateTask)" :show-indicator="false" :height="6" />
      <p v-if="updateTask?.bytesTotal" class="muted">{{ formatBytes(updateTask.bytesDone) }} / {{ formatBytes(updateTask.bytesTotal) }}</p>
      <NButton v-if="updateTask" size="small" :loading="updateCancelling" :disabled="updateCancelling" @click="cancel">取消升级</NButton>
    </div>
    <p v-if="updateInfo?.checkedAt" class="footnote">最近检查：{{ formatTime(updateInfo.checkedAt) }}</p>
    <div class="setting-row">
      <div>
        <strong>软件官网</strong>
        <p class="muted">介绍、更新日志和下载都在 phptools.wuyouzhilian.cn。</p>
      </div>
      <NButton @click="openSite">打开官网</NButton>
    </div>
    <div class="setting-row last">
      <div>
        <strong>提交建议或 BUG</strong>
        <p class="muted">打开 GitHub，按模板填写，也可以附上截图。</p>
      </div>
      <NButton @click="openFeedback">打开 GitHub</NButton>
    </div>
  </NCard>
</template>

<style scoped>
.update-release-heading {
  display: flex;
  align-items: center;
  justify-content: flex-start;
  gap: 12px;
}
.update-release, .update-progress { margin-top: 18px; }
.update-release-notes { white-space: pre-wrap; overflow-wrap: anywhere; }
.update-progress { display: grid; gap: 12px; }
.update-progress > button { justify-self: start; }
.update-progress p { margin: 0; }
</style>
