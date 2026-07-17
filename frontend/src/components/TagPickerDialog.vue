<script setup lang="ts">
import { ref, watch } from 'vue'
import { Tags, X } from '@lucide/vue'
import { DialogClose, DialogContent, DialogDescription, DialogOverlay, DialogPortal, DialogRoot, DialogTitle } from 'reka-ui'
import type { Tag } from '../types'
import UiCheckbox from './ui/UiCheckbox.vue'
import UiSelect from './ui/UiSelect.vue'

const props = withDefaults(defineProps<{
  open: boolean
  tags: Tag[]
  initialSelected?: string[]
  title: string
  bulk?: boolean
  busy?: boolean
}>(), { initialSelected: () => [], bulk: false, busy: false })
const emit = defineEmits<{
  'update:open': [value: boolean]
  save: [payload: { action: 'replace' | 'add' | 'remove'; tagIds: string[] }]
}>()
const selected = ref(new Set<string>())
const action = ref<'add' | 'remove'>('add')
const actionOptions = [{ label: '添加所选标签', value: 'add' }, { label: '移除所选标签', value: 'remove' }]

function toggle(id: string) {
  const next = new Set(selected.value)
  next.has(id) ? next.delete(id) : next.add(id)
  selected.value = next
}

watch(() => props.open, (open) => {
  if (!open) return
  selected.value = new Set(props.initialSelected)
  action.value = 'add'
})
</script>

<template>
  <DialogRoot :open="open" @update:open="emit('update:open', $event)">
    <DialogPortal>
      <DialogOverlay class="dialog-overlay" />
      <DialogContent class="form-dialog tag-picker-dialog">
        <header><span class="form-dialog-icon"><Tags :size="20"/></span><div><DialogTitle>{{ title }}</DialogTitle><DialogDescription>{{ bulk ? '选择要批量添加或移除的标签。' : '勾选后保存，图片标签会与当前选择一致。' }}</DialogDescription></div><DialogClose aria-label="关闭"><X :size="20"/></DialogClose></header>
        <UiSelect v-if="bulk" v-model="action" :options="actionOptions" aria-label="批量标签操作" />
        <div class="tag-picker-list">
          <label v-for="tag in tags" :key="tag.id"><UiCheckbox :model-value="selected.has(tag.id)" :aria-label="tag.name" @update:model-value="toggle(tag.id)"/><i :style="{ background: tag.color }"></i><span>{{ tag.name }}</span><small>{{ tag.image_count }} 张</small></label>
          <p v-if="!tags.length" class="section-note">还没有可用标签，请先在标签管理中创建。</p>
        </div>
        <footer><button class="soft-button" @click="emit('update:open', false)">取消</button><button class="primary-button" :disabled="busy || !selected.size" @click="emit('save', { action: bulk ? action : 'replace', tagIds: [...selected] })">{{ busy ? '保存中…' : '保存标签' }}</button></footer>
      </DialogContent>
    </DialogPortal>
  </DialogRoot>
</template>
