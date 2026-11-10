<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { NAlert, NButton, NCard, NModal, NSpace, NSpin, NTag } from "naive-ui";
import { api } from "../api";
import { useAction } from "../composables";
import { errorText } from "../state";
import type { DependencyReport, RuntimeDependency } from "../types";

const { busy, run } = useAction();
const report = ref<DependencyReport | null>(null);
const checking = ref(false);
const error = ref("");
const guide = ref<RuntimeDependency | null>(null);
const showGuide = ref(false);

async function check() {
  if (checking.value) return;
  checking.value = true;
  error.value = "";
  try {
    report.value = await api.GetRuntimeDependencies();
    if (guide.value) guide.value = report.value.items.find((item) => item.id === guide.value!.id) ?? guide.value;
  } catch (e) {
    error.value = `运行依赖检测失败：${errorText(e)}`;
  } finally {
    checking.value = false;
  }
}

function openInstaller() {
  if (guide.value && !busy.value) void run(() => api.OpenRuntimeInstaller(guide.value!.id));
}

function label(item: RuntimeDependency) {
  if (item.state === "ready") return "已检测到";
  if (item.state === "error") return "检测异常";
  return item.requiredBy.length ? "需要安装" : "未安装 · 按需";
}

const showReady = ref(false);
const readyItems = computed(() => report.value?.items.filter((item) => item.state === "ready") ?? []);
const pendingItems = computed(() => report.value?.items.filter((item) => item.state !== "ready") ?? []);
const shown = computed(() => Boolean(report.value || checking.value || error.value));
const emit = defineEmits<{ visible: [value: boolean] }>();
watch(shown, (value) => emit("visible", value), { immediate: true });
defineExpose({ check });
</script>

<template>
  <NCard v-if="shown" title="运行依赖" :bordered="false" class="panel section-gap runtime-dependencies">
    <template #header-extra>
      <NButton size="small" :loading="checking" :disabled="checking" @click="check">{{ report ? "重新检测" : "检测" }}</NButton>
    </template>
    <p class="muted dependency-intro">
      WebView2 用于软件界面；VC++ 按关联组件安装。安装包选择 x64，完成后重新检测。
    </p>
    <NAlert v-if="error" type="error" class="section-gap">{{ error }}<span v-if="report"> 下方为上次检测结果。</span></NAlert>
    <NSpin v-if="checking && !report" size="small" description="正在读取系统运行库…" />
    <template v-if="report">
      <NAlert v-for="warning in report.warnings" :key="warning" type="warning" class="section-gap">{{ warning }}</NAlert>
      <div v-for="item in pendingItems" :key="item.id" :data-dependency-id="item.id" class="dependency-row">
        <div class="dependency-info">
          <div class="dependency-heading">
            <strong>{{ item.name }}</strong>
            <NTag size="small" :bordered="false" :type="item.state === 'error' ? 'error' : item.requiredBy.length ? 'warning' : 'default'">{{ label(item) }}</NTag>
          </div>
          <p v-if="item.version" class="muted">检测版本：{{ item.version }}</p>
          <p class="muted">{{ item.message }}</p>
          <p class="muted">{{ item.requiredBy.length ? `关联：${item.requiredBy.join('、')}` : '当前已安装组件未检测到此系统运行库需求，可暂不安装。' }}</p>
        </div>
        <NButton size="small" type="primary" secondary @click="guide = item; showGuide = true">修复方法</NButton>
      </div>
      <div v-if="readyItems.length" class="dependency-passed">
        <span>已检测到 {{ readyItems.length }} 项</span>
        <NButton size="small" quaternary @click="showReady = !showReady">{{ showReady ? "收起" : "展开" }}</NButton>
      </div>
      <template v-if="showReady">
        <div v-for="item in readyItems" :key="item.id" :data-dependency-id="item.id" class="dependency-row">
          <div class="dependency-info">
            <div class="dependency-heading">
              <strong>{{ item.name }}</strong>
              <NTag size="small" :bordered="false" type="success">{{ label(item) }}</NTag>
            </div>
            <p v-if="item.version" class="muted">检测版本：{{ item.version }}</p>
            <p class="muted">{{ item.message }}</p>
            <p class="muted">{{ item.requiredBy.length ? `关联：${item.requiredBy.join('、')}` : '当前已安装组件未检测到此系统运行库需求，可暂不安装。' }}</p>
          </div>
          <NButton size="small" @click="guide = item; showGuide = true">官方安装指引</NButton>
        </div>
      </template>
      <p class="footnote">
        检测时间：{{ new Date(report.checkedAt).toLocaleString('zh-CN', { hour12: false }) }}。检查系统运行库文件及已安装组件的主要程序、随附 DLL 和默认 PHP 扩展；自定义扩展与组件实际启动结果需另行核对。
      </p>
    </template>
  </NCard>
  <NModal
    v-model:show="showGuide" preset="card" title="运行依赖修复方法"
    class="dependency-guide-modal"
  >
    <template v-if="guide">
      <strong>{{ guide.name }}</strong>
      <p class="muted">当前检测结果：{{ label(guide) }}{{ guide.version ? `（${guide.version}）` : '' }}</p>
      <NAlert v-if="error" type="error" class="section-gap">{{ error }}</NAlert>
      <p class="dependency-guide-text">{{ guide.guide }}</p>
      <NAlert type="info" :show-icon="false">
        点击下方按钮打开微软官网，下载匹配的 x64 安装程序。缺失时选择安装；已安装但异常时，运行安装程序提供的“修复”选项。完成后点击“安装后重新检测”核对结果。
      </NAlert>
      <p v-if="guide.id !== 'webview2'" class="muted">其他年代或 x86 运行库不能替代这里的 x64 版本。</p>
    </template>
    <template #footer>
      <NSpace justify="end">
        <NButton :disabled="checking" @click="showGuide = false">关闭</NButton>
        <NButton :loading="checking" :disabled="checking" @click="check">安装后重新检测</NButton>
        <NButton type="primary" :loading="busy" @click="openInstaller">打开微软下载页</NButton>
      </NSpace>
    </template>
  </NModal>
</template>
