<script setup lang="ts">
import { computed, reactive, ref, watch } from "vue";
import { NAlert, NButton, NCard, NForm, NFormItem, NInput, NModal, NProgress, NRadio, NRadioGroup, NTag } from "naive-ui";
import { api, type FeedbackReport } from "../api";
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
const feedbackOpen = ref(false);
const feedbackBusy = ref(false);
const imagesLoading = ref(0);
const feedbackError = ref("");
const images = ref<{ url: string; data: string }[]>([]);
const fileInput = ref<HTMLInputElement | null>(null);
const feedback = reactive({
  kind: "bug" as "bug" | "suggestion",
  title: "",
  version: "",
  steps: "1. \n2. \n3. ",
  expected: "",
  actual: "",
  problem: "",
  proposal: "",
  contact: "",
});
const templateText = computed(() => feedback.kind === "bug"
  ? "1. 操作步骤：从哪个页面开始，点了什么\n2. 期望结果：你认为应该发生什么\n3. 实际结果：实际看到了什么\n可以附上截图，最多 3 张。图片会压缩到 1 MB 以内。"
  : "1. 现在哪里不方便\n2. 希望软件改成什么样\n可以附上截图，最多 3 张。图片会压缩到 1 MB 以内。");

watch(() => feedback.kind, (kind) => {
  if (kind === "bug" && feedback.steps.trim() === "") feedback.steps = "1. \n2. \n3. ";
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
  feedbackError.value = "";
  if (!feedback.version && currentVersion.value) feedback.version = currentVersion.value;
  feedbackOpen.value = true;
}
function textLength(value: string) {
  return Array.from(value.trim()).length;
}
function addImages(list: FileList | null) {
  feedbackError.value = "";
  const files = list ? Array.from(list) : [];
  if (fileInput.value) fileInput.value.value = "";
  if (images.value.length + files.length > 3) {
    feedbackError.value = "最多上传 3 张图片。";
    return;
  }
  for (const file of files) {
    const allowed = ["image/jpeg", "image/png", "image/gif", "image/webp"].includes(file.type)
      || /\.(jpe?g|png|gif|webp)$/i.test(file.name);
    if (!allowed) {
      feedbackError.value = "图片只接受 JPG、PNG、GIF 或 WebP。";
      return;
    }
    if (file.size > 8 * 1024 * 1024) {
      feedbackError.value = "单张原图不能超过 8 MB。";
      return;
    }
  }
  for (const file of files) {
    imagesLoading.value += 1;
    void compressFeedbackImage(file).then((image) => {
      images.value.push(image);
    }).catch((error: unknown) => {
      feedbackError.value = errorText(error);
    }).finally(() => {
      imagesLoading.value -= 1;
    });
  }
}
async function compressFeedbackImage(file: File) {
  let bitmap: ImageBitmap;
  try {
    bitmap = await createImageBitmap(file);
  } catch {
    throw new Error("有一张图片无法读取。");
  }
  try {
    if (bitmap.width < 1 || bitmap.height < 1 || bitmap.width > 8000 || bitmap.height > 8000 || bitmap.width * bitmap.height > 8_000_000) {
      throw new Error("图片尺寸过大。");
    }
    let edge = 1920;
    let quality = 0.85;
    let blob = await renderFeedbackImage(bitmap, edge, quality);
    for (let attempt = 0; attempt < 12 && blob.size > 1024 * 1024; attempt += 1) {
      if (quality > 0.55) {
        quality = Math.round((quality - 0.1) * 100) / 100;
      } else {
        edge = Math.floor(edge * 0.75);
        quality = 0.75;
        if (edge < 480) break;
      }
      blob = await renderFeedbackImage(bitmap, edge, quality);
    }
    if (blob.size > 1024 * 1024) throw new Error("这张图片无法压到 1 MB 以内。");
    const bytes = new Uint8Array(await blob.arrayBuffer());
    return { url: URL.createObjectURL(blob), data: encodeBase64(bytes) };
  } finally {
    bitmap.close();
  }
}
function renderFeedbackImage(bitmap: ImageBitmap, maxEdge: number, quality: number) {
  const scale = Math.min(1, maxEdge / Math.max(bitmap.width, bitmap.height));
  const width = Math.max(1, Math.round(bitmap.width * scale));
  const height = Math.max(1, Math.round(bitmap.height * scale));
  const canvas = document.createElement("canvas");
  canvas.width = width;
  canvas.height = height;
  const context = canvas.getContext("2d");
  if (!context) return Promise.reject(new Error("有一张图片无法读取。"));
  context.fillStyle = "#ffffff";
  context.fillRect(0, 0, width, height);
  context.drawImage(bitmap, 0, 0, width, height);
  return new Promise<Blob>((resolve, reject) => {
    canvas.toBlob((blob) => blob ? resolve(blob) : reject(new Error("有一张图片无法读取。")), "image/jpeg", quality);
  });
}
function encodeBase64(bytes: Uint8Array) {
  let binary = "";
  for (let index = 0; index < bytes.length; index += 0x8000) {
    binary += String.fromCharCode(...bytes.subarray(index, index + 0x8000));
  }
  return btoa(binary);
}
function removeImage(index: number) {
  const [image] = images.value.splice(index, 1);
  if (image) URL.revokeObjectURL(image.url);
}
function resetFeedback() {
  feedback.kind = "bug";
  feedback.title = "";
  feedback.steps = "1. \n2. \n3. ";
  feedback.expected = "";
  feedback.actual = "";
  feedback.problem = "";
  feedback.proposal = "";
  feedback.contact = "";
  for (const image of images.value) URL.revokeObjectURL(image.url);
  images.value = [];
}
async function submitFeedback() {
  feedbackError.value = "";
  if (imagesLoading.value > 0) {
    feedbackError.value = "图片还在读取，请稍后再提交。";
    return;
  }
  if (textLength(feedback.title) < 2) {
    feedbackError.value = "请用一句话写清标题。";
    return;
  }
  if (feedback.kind === "bug" && (textLength(feedback.steps) < 4 || textLength(feedback.expected) < 4 || textLength(feedback.actual) < 4)) {
    feedbackError.value = "BUG 需要写清操作步骤、期望结果和实际结果。";
    return;
  }
  if (feedback.kind === "suggestion" && (textLength(feedback.problem) < 4 || textLength(feedback.proposal) < 4)) {
    feedbackError.value = "建议需要写清现在的问题和希望的做法。";
    return;
  }
  const report: FeedbackReport = {
    kind: feedback.kind,
    title: feedback.title.trim(),
    version: feedback.version.trim(),
    steps: feedback.kind === "bug" ? feedback.steps.trim() : "",
    expected: feedback.kind === "bug" ? feedback.expected.trim() : "",
    actual: feedback.kind === "bug" ? feedback.actual.trim() : "",
    problem: feedback.kind === "suggestion" ? feedback.problem.trim() : "",
    proposal: feedback.kind === "suggestion" ? feedback.proposal.trim() : "",
    contact: feedback.contact.trim(),
    images: images.value.map((image) => ({ data: image.data })),
  };
  feedbackBusy.value = true;
  try {
    const id = await api.SubmitFeedback(report);
    resetFeedback();
    feedbackOpen.value = false;
    message.success(`已提交，编号 ${id}。`);
  } catch (error) {
    feedbackError.value = errorText(error);
  } finally {
    feedbackBusy.value = false;
  }
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
        <p class="muted">按模板写清问题，最多 3 张图片，提交时会压缩到 1 MB 以内。</p>
      </div>
      <NButton @click="openFeedback">填写并提交</NButton>
    </div>
  </NCard>

  <NModal v-model:show="feedbackOpen" preset="card" title="提交建议或 BUG" :style="{ width: '640px' }" :mask-closable="!feedbackBusy" :close-on-esc="!feedbackBusy">
    <NForm label-placement="top">
      <NFormItem label="类型">
        <NRadioGroup v-model:value="feedback.kind" :disabled="feedbackBusy">
          <NRadio value="bug">BUG</NRadio>
          <NRadio value="suggestion">建议</NRadio>
        </NRadioGroup>
      </NFormItem>
      <pre class="feedback-template">{{ templateText }}</pre>
      <NFormItem label="标题" required>
        <NInput v-model:value="feedback.title" maxlength="60" placeholder="一句话说明问题" :disabled="feedbackBusy" />
      </NFormItem>
      <NFormItem label="软件版本">
        <NInput v-model:value="feedback.version" maxlength="80" placeholder="例如 0.1.32" :disabled="feedbackBusy" />
      </NFormItem>
      <template v-if="feedback.kind === 'bug'">
        <NFormItem label="操作步骤" required>
          <NInput v-model:value="feedback.steps" type="textarea" maxlength="2000" :autosize="{ minRows: 4, maxRows: 8 }" placeholder="1. 打开哪个页面&#10;2. 做了什么&#10;3. 出现了什么" :disabled="feedbackBusy" />
        </NFormItem>
        <NFormItem label="期望结果" required>
          <NInput v-model:value="feedback.expected" type="textarea" maxlength="2000" :autosize="{ minRows: 2, maxRows: 6 }" placeholder="你认为这时应该发生什么" :disabled="feedbackBusy" />
        </NFormItem>
        <NFormItem label="实际结果" required>
          <NInput v-model:value="feedback.actual" type="textarea" maxlength="2000" :autosize="{ minRows: 2, maxRows: 6 }" placeholder="实际看到的提示、界面或结果" :disabled="feedbackBusy" />
        </NFormItem>
      </template>
      <template v-else>
        <NFormItem label="现在的问题" required>
          <NInput v-model:value="feedback.problem" type="textarea" maxlength="2000" :autosize="{ minRows: 3, maxRows: 8 }" placeholder="现在哪里不方便" :disabled="feedbackBusy" />
        </NFormItem>
        <NFormItem label="希望的做法" required>
          <NInput v-model:value="feedback.proposal" type="textarea" maxlength="2000" :autosize="{ minRows: 3, maxRows: 8 }" placeholder="希望软件改成什么样" :disabled="feedbackBusy" />
        </NFormItem>
      </template>
      <NFormItem label="图片">
        <input ref="fileInput" class="feedback-file" type="file" accept="image/jpeg,image/png,image/gif,image/webp" multiple :disabled="feedbackBusy" @change="addImages(($event.target as HTMLInputElement).files)">
        <div v-if="images.length" class="feedback-previews">
          <figure v-for="(image, index) in images" :key="image.url">
            <img :src="image.url" alt="">
            <NButton size="tiny" quaternary :disabled="feedbackBusy" @click="removeImage(index)">移除</NButton>
          </figure>
        </div>
      </NFormItem>
      <NFormItem label="联系方式（可选）">
        <NInput v-model:value="feedback.contact" maxlength="80" placeholder="邮箱或其他方式，可以不填" :disabled="feedbackBusy" />
      </NFormItem>
      <NAlert v-if="feedbackError" type="error" :show-icon="false">{{ feedbackError }}</NAlert>
    </NForm>
    <template #footer>
      <NButton type="primary" :loading="feedbackBusy" @click="submitFeedback">提交</NButton>
    </template>
  </NModal>
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
.feedback-template {
  margin: 0 0 16px;
  padding: 12px 14px;
  border-radius: 8px;
  background: #f4f7f8;
  color: #3d5160;
  white-space: pre-wrap;
  font: inherit;
  font-size: 12px;
  line-height: 1.6;
}
.feedback-file { font-size: 12px; }
.feedback-previews {
  display: flex;
  gap: 10px;
  margin-top: 10px;
}
.feedback-previews figure { margin: 0; }
.feedback-previews img {
  display: block;
  width: 96px;
  height: 72px;
  object-fit: cover;
  border-radius: 6px;
  border: 1px solid #e4eaee;
}
</style>
