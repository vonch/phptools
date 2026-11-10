<script setup lang="ts">
import { ref, watch } from "vue";
import { NButton, NIcon, NInput, NModal, NSpace, NSwitch, NTag } from "naive-ui";
import { ArrowUpOutline, ArrowDownOutline } from "@vicons/ionicons5";
import { api } from "../api";
import { useAction } from "../composables";
import { snapshot } from "../state";
import { defaultSidebar, pageInfo, sidebarLabel } from "../sidebar";
import type { SidebarItem } from "../types";

const props = defineProps<{ show: boolean }>();
const emit = defineEmits<{ "update:show": [value: boolean] }>();
const { busy, run } = useAction();
const draft = ref<SidebarItem[]>([]);

watch(() => props.show, (show) => {
  if (show) {
    draft.value = (snapshot.value?.sidebar ?? defaultSidebar())
      .map((item) => ({ ...item, label: item.label ?? "" }));
  }
}, { immediate: true });

function close() {
  if (!busy.value) emit("update:show", false);
}

function move(index: number, offset: number) {
  const target = index + offset;
  if (busy.value || target < 0 || target >= draft.value.length) return;
  const item = draft.value.splice(index, 1)[0]!;
  draft.value.splice(target, 0, item);
}

async function save() {
  if (busy.value) return;
  const saved = await run(async () => {
    await api.SaveSidebar(draft.value.map((item) => ({
      ...item,
      label: item.label.trim(),
    })));
    return true;
  }, "侧栏菜单已保存");
  if (saved) emit("update:show", false);
}
</script>

<template>
  <NModal
    :show="show"
    preset="card"
    title="侧栏菜单管理"
    class="sidebar-manager-modal"
    :closable="!busy"
    :mask-closable="!busy"
    :close-on-esc="!busy"
    @update:show="close"
  >
    <p class="sidebar-manager-hint muted">
      可修改菜单文字，使用箭头调整顺序，关闭开关即可隐藏。留空会恢复默认名称，保存后生效并在重启后保留。
    </p>
    <ol class="sidebar-manager-list" aria-label="侧栏菜单顺序">
      <li
        v-for="(item, index) in draft"
        :key="item.key"
        :data-menu-key="item.key"
        class="sidebar-manager-row"
        :class="{ 'is-hidden': item.hidden }"
      >
        <span class="sidebar-manager-position">{{ index + 1 }}</span>
        <NInput
          v-model:value="item.label"
          size="small"
          class="sidebar-manager-input"
          :maxlength="32"
          :placeholder="pageInfo(item.key).label"
          :aria-label="`修改${pageInfo(item.key).label}菜单文字`"
        />
        <NTag v-if="item.key === 'settings'" size="small" :bordered="false">
          始终显示
        </NTag>
        <NSpace :size="4" class="sidebar-manager-arrows">
          <NButton
            size="small" quaternary
            :disabled="busy || index === 0"
            :aria-label="`上移${sidebarLabel(item)}`"
            title="上移"
            @click="move(index, -1)"
          ><template #icon><NIcon><ArrowUpOutline /></NIcon></template></NButton>
          <NButton
            size="small" quaternary
            :disabled="busy || index === draft.length - 1"
            :aria-label="`下移${sidebarLabel(item)}`"
            title="下移"
            @click="move(index, 1)"
          ><template #icon><NIcon><ArrowDownOutline /></NIcon></template></NButton>
        </NSpace>
        <NSwitch
          :value="!item.hidden"
          :disabled="busy || item.key === 'settings'"
          :aria-label="`${item.hidden ? '显示' : '隐藏'}${sidebarLabel(item)}`"
          @update:value="item.hidden = !$event"
        >
          <template #checked>显示</template>
          <template #unchecked>隐藏</template>
        </NSwitch>
      </li>
    </ol>
    <p class="muted sidebar-manager-note">“设置”始终显示，方便重新开启隐藏的菜单。隐藏菜单不会停止服务。</p>
    <template #footer>
      <div class="row-between">
        <NButton :disabled="busy" @click="draft = defaultSidebar()">恢复默认</NButton>
        <NSpace>
          <NButton :disabled="busy" @click="close">取消</NButton>
          <NButton type="primary" :loading="busy" @click="save">保存</NButton>
        </NSpace>
      </div>
    </template>
  </NModal>
</template>
