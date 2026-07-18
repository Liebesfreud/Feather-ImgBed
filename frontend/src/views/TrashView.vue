<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ArchiveRestore, CheckSquare, Eraser, ImageOff, ListChecks, LoaderCircle, RefreshCw, ShieldAlert, Trash2 } from '@lucide/vue'
import { api, deleteJSON, postJSON } from '../api'
import { toast } from '../toast'
import type { ImageItem } from '../types'
import ConfirmDialog from '../components/ui/ConfirmDialog.vue'
import UiCheckbox from '../components/ui/UiCheckbox.vue'
import ImageManagementNav from '../components/ImageManagementNav.vue'

type DangerAction =
  | { kind: 'single'; item: ImageItem }
  | { kind: 'selected' }
  | { kind: 'all' }

const images = ref<ImageItem[]>([])
const cursor = ref('')
const loading = ref(true)
const loadingMore = ref(false)
const failed = ref(false)
const selected = ref(new Set<string>())
const selectMode = ref(false)
const working = ref(false)
const dangerOpen = ref(false)
const dangerAction = ref<DangerAction | null>(null)

const dangerTitle = computed(() => {
  if (dangerAction.value?.kind === 'single') return '永久删除这张图片？'
  if (dangerAction.value?.kind === 'selected') return `永久删除 ${selected.value.size} 张图片？`
  return '清空回收站？'
})
const dangerDescription = computed(() => dangerAction.value?.kind === 'single'
  ? `“${dangerAction.value.item.original_name}”的原图和派生文件都将被删除，无法恢复。`
  : '存储中的原图和派生文件都将被删除。失败的项目会保留在回收站中供稍后重试。')

async function load(reset = false) {
  if (reset) { loading.value = true; cursor.value = ''; images.value = [] } else loadingMore.value = true
  failed.value = false
  try {
    const params = new URLSearchParams({ limit: '24' })
    if (!reset && cursor.value) params.set('cursor', cursor.value)
    const result = await api<{ items: ImageItem[]; next_cursor: string }>(`/trash?${params}`)
    images.value = reset ? result.items : [...images.value, ...result.items]
    cursor.value = result.next_cursor
  } catch { failed.value = true }
  finally { loading.value = false; loadingMore.value = false }
}
function toggleSelected(id: string) {
  const next = new Set(selected.value)
  next.has(id) ? next.delete(id) : next.add(id)
  selected.value = next
}
function clearSelection() { selected.value = new Set() }
function selectLoaded() { selected.value = new Set(images.value.map((item) => item.id)) }
function formatSize(bytes: number) { return bytes >= 1024 * 1024 ? `${(bytes / 1024 / 1024).toFixed(2)} MB` : `${Math.max(1, bytes / 1024).toFixed(0)} KB` }
function formatDate(value?: string) {
  return value
    ? new Intl.DateTimeFormat('zh-CN', { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(value))
    : '未知时间'
}
async function restore(items: ImageItem[]) {
  if (!items.length || working.value) return
  working.value = true
  const results = await Promise.allSettled(items.map((item) => postJSON(`/trash/${item.id}/restore`)))
  const restoredIDs = new Set(items.filter((_, index) => results[index].status === 'fulfilled').map((item) => item.id))
  images.value = images.value.filter((item) => !restoredIDs.has(item.id))
  selected.value = new Set([...selected.value].filter((id) => !restoredIDs.has(id)))
  const failedCount = results.length - restoredIDs.size
  toast(`已恢复 ${restoredIDs.size} 张图片${failedCount ? `，${failedCount} 张失败` : ''}`, failedCount ? 'error' : 'success')
  working.value = false
}
function requestDanger(action: DangerAction) {
  dangerAction.value = action
  dangerOpen.value = true
}
async function confirmDanger() {
  const action = dangerAction.value
  if (!action) return
  working.value = true
  try {
    if (action.kind === 'single') {
      await deleteJSON(`/trash/${action.item.id}`)
      toast('图片已永久删除')
    } else {
      if (action.kind === 'selected') {
        await postJSON('/trash/purge', { ids: [...selected.value] })
        toast('批量清理已完成')
      } else {
        let removed = 0
        for (;;) {
          const result = await postJSON<{ succeeded: number; failed: number; remaining: number }>('/trash/purge', { all: true })
          removed += result.succeeded
          if (result.failed || !result.remaining) {
            toast(result.failed ? `已清理 ${removed} 张，失败项目已保留供重试` : `回收站清理已完成，共删除 ${removed} 张`, result.failed ? 'error' : 'success')
            break
          }
        }
      }
    }
    clearSelection()
    await load(true)
  } catch (error) {
    toast(error instanceof Error ? error.message : '永久删除失败', 'error')
    await load(true)
  } finally {
    working.value = false
    dangerOpen.value = false
    dangerAction.value = null
  }
}

onMounted(() => void load(true))
</script>

<template>
  <section class="content-stack trash-view">
    <ImageManagementNav />
    <header class="page-heading gallery-heading">
      <h1>回收站</h1>
      <div class="trash-heading-actions">
        <button v-if="images.length" class="soft-button danger" @click="requestDanger({ kind: 'all' })"><Trash2 :size="17"/>清空回收站</button>
        <button class="soft-button manage-button" :class="{ active: selectMode }" @click="selectMode = !selectMode; clearSelection()"><CheckSquare :size="17"/>{{ selectMode ? '完成管理' : '批量管理' }}</button>
      </div>
    </header>

    <div v-if="loading" class="gallery-state"><LoaderCircle class="spin" :size="28"/><p>正在读取回收站…</p></div>
    <div v-else-if="failed" class="gallery-state"><ImageOff :size="38"/><h2>回收站暂时无法加载</h2><button class="soft-button" @click="load(true)"><RefreshCw :size="17"/>重新加载</button></div>
    <div v-else-if="!images.length" class="gallery-state empty-state"><div class="empty-art"><Trash2 :size="44"/></div><h2>回收站是空的</h2><p>从图库删除的图片会暂时保留在这里。</p></div>
    <div v-else class="image-grid trash-grid">
      <article v-for="item in images" :key="item.id" class="image-card trash-card" :class="{ selected: selected.has(item.id) }" @click="selectMode && toggleSelected(item.id)">
        <div class="image-frame" :style="{ aspectRatio: `${item.width || 4} / ${item.height || 3}` }">
          <img :src="item.thumbnail_url || item.url" :alt="item.original_name" loading="lazy" decoding="async">
          <UiCheckbox v-if="selectMode" class="select-check" :model-value="selected.has(item.id)" :aria-label="`选择 ${item.original_name}`" @click.stop @update:model-value="toggleSelected(item.id)" />
        </div>
        <div class="trash-card-body">
          <strong :title="item.original_name">{{ item.original_name }}</strong>
          <span>删除于 {{ formatDate(item.deleted_at) }} · {{ formatSize(item.size) }}</span>
          <p v-if="item.purge_error" class="purge-error"><ShieldAlert :size="15"/>{{ item.purge_error }}</p>
          <div class="trash-card-actions">
            <button :disabled="working" @click.stop="restore([item])"><ArchiveRestore :size="16"/>恢复</button>
            <button class="danger" :disabled="working" @click.stop="requestDanger({ kind: 'single', item })"><Trash2 :size="16"/>永久删除</button>
          </div>
        </div>
      </article>
    </div>
    <button v-if="cursor && !loading" class="load-more" :disabled="loadingMore" @click="load(false)"><LoaderCircle v-if="loadingMore" class="spin" :size="17"/>{{ loadingMore ? '正在加载…' : '加载更多' }}</button>
    <p v-else-if="images.length" class="gallery-end">已显示全部图片</p>

    <aside v-if="selectMode" class="batch-bar" aria-label="回收站批量操作栏">
      <strong>已选择 {{ selected.size }} 张</strong>
      <span class="batch-spacer"></span>
      <button @click="selectLoaded"><ListChecks :size="16"/>选择已加载</button>
      <button :disabled="!selected.size" @click="clearSelection"><Eraser :size="16"/>清空</button>
      <span class="batch-divider"></span>
      <button :disabled="!selected.size || working" @click="restore(images.filter(item => selected.has(item.id)))"><ArchiveRestore :size="16"/>批量恢复</button>
      <button class="danger" :disabled="!selected.size || working" @click="requestDanger({ kind: 'selected' })"><Trash2 :size="16"/>永久删除</button>
    </aside>

    <ConfirmDialog v-model:open="dangerOpen" :title="dangerTitle" :description="dangerDescription" confirm-label="永久删除" :busy="working" @confirm="confirmDanger" />
  </section>
</template>
