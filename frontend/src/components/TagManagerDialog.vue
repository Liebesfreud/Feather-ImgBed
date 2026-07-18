<script setup lang="ts">
import { ref, watch } from 'vue'
import { LoaderCircle, Plus, Save, Tags, Trash2, X } from '@lucide/vue'
import { DialogClose, DialogContent, DialogOverlay, DialogPortal, DialogRoot, DialogTitle } from 'reka-ui'
import { api, deleteJSON, postJSON, putJSON } from '../api'
import { toast } from '../toast'
import type { Tag } from '../types'

const props = defineProps<{ open: boolean }>()
const emit = defineEmits<{ 'update:open': [value: boolean]; changed: [tags: Tag[]] }>()
const tags = ref<Tag[]>([])
const loading = ref(false)
const saving = ref('')
const name = ref('')
const color = ref('#1db954')
const drafts = ref<Record<string, { name: string; color: string }>>({})

async function load() {
  loading.value = true
  try {
    tags.value = await api<Tag[]>('/tags')
    drafts.value = Object.fromEntries(tags.value.map((tag) => [tag.id, { name: tag.name, color: tag.color }]))
    emit('changed', tags.value)
  } catch (error) { toast(error instanceof Error ? error.message : '读取标签失败', 'error') }
  finally { loading.value = false }
}
async function createTag() {
  if (!name.value.trim()) return
  saving.value = 'new'
  try {
    await postJSON<Tag>('/tags', { name: name.value.trim(), color: color.value })
    name.value = ''
    await load()
    toast('标签已创建')
  } catch (error) { toast(error instanceof Error ? error.message : '创建标签失败', 'error') }
  finally { saving.value = '' }
}
async function saveTag(tag: Tag) {
  saving.value = tag.id
  try {
    await putJSON<Tag>(`/tags/${tag.id}`, drafts.value[tag.id])
    await load()
    toast('标签已更新')
  } catch (error) { toast(error instanceof Error ? error.message : '更新标签失败', 'error') }
  finally { saving.value = '' }
}
async function removeTag(tag: Tag) {
  if (!window.confirm(`删除标签“${tag.name}”？图片不会被删除。`)) return
  saving.value = tag.id
  try {
    await deleteJSON(`/tags/${tag.id}`)
    await load()
    toast('标签已删除')
  } catch (error) { toast(error instanceof Error ? error.message : '删除标签失败', 'error') }
  finally { saving.value = '' }
}

watch(() => props.open, (open) => { if (open) void load() })
</script>

<template>
  <DialogRoot :open="open" @update:open="emit('update:open', $event)">
    <DialogPortal>
      <DialogOverlay class="dialog-overlay" />
      <DialogContent class="form-dialog tag-manager-dialog" :aria-describedby="undefined">
        <header><span class="form-dialog-icon"><Tags :size="20"/></span><div><DialogTitle>标签管理</DialogTitle></div><DialogClose aria-label="关闭"><X :size="20"/></DialogClose></header>
        <form class="tag-create" @submit.prevent="createTag"><input v-model.trim="name" maxlength="50" placeholder="新标签名称" aria-label="新标签名称"><input v-model="color" type="color" aria-label="新标签颜色"><button class="primary-button" :disabled="saving === 'new'"><Plus :size="16"/>创建</button></form>
        <div v-if="loading" class="dialog-loading"><LoaderCircle class="spin" :size="22"/>正在读取标签…</div>
        <div v-else class="tag-editor-list">
          <article v-for="tag in tags" :key="tag.id">
            <input v-model.trim="drafts[tag.id].name" maxlength="50" :aria-label="`${tag.name}名称`">
            <input v-model="drafts[tag.id].color" type="color" :aria-label="`${tag.name}颜色`">
            <span>{{ tag.image_count }} 张</span>
            <button :aria-label="`保存${tag.name}`" :disabled="saving === tag.id" @click="saveTag(tag)"><Save :size="16"/></button>
            <button class="danger" :aria-label="`删除${tag.name}`" :disabled="saving === tag.id" @click="removeTag(tag)"><Trash2 :size="16"/></button>
          </article>
          <p v-if="!tags.length" class="section-note">还没有标签</p>
        </div>
      </DialogContent>
    </DialogPortal>
  </DialogRoot>
</template>
