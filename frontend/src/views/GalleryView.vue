<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { Search, Database, CalendarDays, ArrowDownUp, CheckSquare, MoreHorizontal, ImageOff, LoaderCircle, X, ChevronLeft, ChevronRight, ZoomIn, ZoomOut, Link2, FileCode2, Code2, Trash2, UploadCloud, RefreshCw } from '@lucide/vue'
import { useRouter } from 'vue-router'
import { DialogClose, DialogContent, DialogDescription, DialogOverlay, DialogPortal, DialogRoot, DialogTitle } from 'reka-ui'
import { api, deleteJSON } from '../api'
import { toast } from '../toast'
import type { ImageItem, StorageRecord } from '../types'
import ConfirmDialog from '../components/ui/ConfirmDialog.vue'
import UiCheckbox from '../components/ui/UiCheckbox.vue'
import UiSelect from '../components/ui/UiSelect.vue'
import UiTooltip from '../components/ui/UiTooltip.vue'

const router = useRouter()
const images = ref<ImageItem[]>([])
const storages = ref<StorageRecord[]>([])
const cursor = ref('')
const loading = ref(true)
const loadingMore = ref(false)
const failed = ref(false)
const search = ref('')
const storage = ref('')
const from = ref('')
const selected = ref(new Set<string>())
const selectMode = ref(false)
const previewIndex = ref(-1)
const zoom = ref(1)
const deleting = ref(false)
const deleteConfirmOpen = ref(false)
let searchTimer = 0

const preview = computed(() => images.value[previewIndex.value])
const hasFilters = computed(() => Boolean(search.value || storage.value || from.value))
const storageFilterValue = computed({ get: () => storage.value || '__all__', set: (value: string) => { storage.value = value === '__all__' ? '' : value } })
const storageOptions = computed(() => [{ label: '全部存储', value: '__all__' }, ...storages.value.map((item) => ({ label: item.name, value: item.id }))])

async function load(reset = false) {
  if (reset) { loading.value = true; images.value = []; cursor.value = '' } else loadingMore.value = true
  failed.value = false
  try {
    const params = new URLSearchParams({ limit: '24' })
    if (!reset && cursor.value) params.set('cursor', cursor.value)
    if (search.value) params.set('search', search.value)
    if (storage.value) params.set('storage_id', storage.value)
    if (from.value) params.set('from', new Date(`${from.value}T00:00:00`).toISOString())
    const result = await api<{ items: ImageItem[]; next_cursor: string }>(`/images?${params}`)
    images.value = reset ? result.items : [...images.value, ...result.items]
    cursor.value = result.next_cursor
  } catch { failed.value = true }
  finally { loading.value = false; loadingMore.value = false }
}
function clearFilters() { search.value = ''; storage.value = ''; from.value = ''; void load(true) }
function toggleSelected(id: string) { const next = new Set(selected.value); next.has(id) ? next.delete(id) : next.add(id); selected.value = next }
function openPreview(index: number) { if (selectMode.value) { toggleSelected(images.value[index].id); return }; previewIndex.value = index; zoom.value = 1 }
function closePreview() { deleteConfirmOpen.value = false; previewIndex.value = -1 }
function move(delta: number) { if (!images.value.length) return; previewIndex.value = (previewIndex.value + delta + images.value.length) % images.value.length; zoom.value = 1 }
function formatSize(bytes: number) { return bytes >= 1024 * 1024 ? `${(bytes / 1024 / 1024).toFixed(2)} MB` : `${Math.max(1, bytes / 1024).toFixed(0)} KB` }
function formatDate(value: string) { return new Intl.DateTimeFormat('zh-CN', { year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' }).format(new Date(value)) }
async function copy(item: ImageItem, kind: 'url' | 'markdown' | 'html') {
  const value = kind === 'markdown' ? `![${item.original_name}](${item.url})` : kind === 'html' ? `<img src="${item.url}" alt="${item.original_name}">` : item.url
  await navigator.clipboard.writeText(value); toast('已复制到剪贴板')
}
async function remove(item: ImageItem) {
  deleting.value = true
  try { await deleteJSON(`/images/${item.id}`); images.value = images.value.filter((image) => image.id !== item.id); closePreview(); toast('图片已删除') }
  catch (e) { toast(e instanceof Error ? e.message : '删除失败', 'error') }
  finally { deleting.value = false }
}
function onKey(event: KeyboardEvent) {
  if (!preview.value) return
  if (event.key === 'ArrowLeft') move(-1)
  if (event.key === 'ArrowRight') move(1)
  if (event.key === '+' || event.key === '=') zoom.value = Math.min(2.5, zoom.value + .25)
  if (event.key === '-') zoom.value = Math.max(.5, zoom.value - .25)
}
watch(search, () => { clearTimeout(searchTimer); searchTimer = window.setTimeout(() => load(true), 350) })
watch([storage, from], () => load(true))
onMounted(async () => { window.addEventListener('keydown', onKey); storages.value = await api<StorageRecord[]>('/storages'); await load(true) })
onBeforeUnmount(() => window.removeEventListener('keydown', onKey))
</script>

<template>
  <section class="content-stack gallery-view">
    <header class="page-heading gallery-heading"><div><h1>图片管理</h1><p>查找、预览并管理所有已上传的图片。</p></div><span v-if="images.length">{{ images.length }} 张图片</span></header>
    <div class="gallery-toolbar">
      <label class="search-control"><Search :size="18"/><input v-model.trim="search" placeholder="搜索图片名称" aria-label="搜索图片名称"></label>
      <div class="gallery-select"><Database :size="17"/><UiSelect v-model="storageFilterValue" :options="storageOptions" aria-label="按存储筛选" /></div>
      <label><CalendarDays :size="17"/><input v-model="from" type="date" aria-label="起始日期"></label>
      <label class="sort-control"><ArrowDownUp :size="17"/>最新上传</label>
      <button class="soft-button manage-button" :class="{ active: selectMode }" @click="selectMode = !selectMode; selected.clear()"><CheckSquare :size="17"/>{{ selectMode ? '完成管理' : '批量管理' }}</button>
    </div>
    <div v-if="loading" class="gallery-state"><LoaderCircle class="spin" :size="28"/><p>正在整理图库…</p></div>
    <div v-else-if="failed" class="gallery-state"><ImageOff :size="38"/><h2>图库暂时无法加载</h2><p>筛选条件已为你保留，请稍后重试。</p><button class="soft-button" @click="load(true)"><RefreshCw :size="17"/>重新加载</button></div>
    <div v-else-if="!images.length" class="gallery-state empty-state"><div class="empty-art"><ImageOff :size="46"/></div><h2>{{ hasFilters ? '没有找到匹配的图片' : '图库还是空的' }}</h2><p>{{ hasFilters ? '试试更换关键词或调整筛选条件。' : '上传第一张图片，开始构建你的灵感空间。' }}</p><button v-if="hasFilters" class="soft-button" @click="clearFilters">清除筛选</button><button v-else class="primary-button" @click="router.push('/upload')"><UploadCloud :size="18"/>上传第一张图片</button></div>
    <div v-else class="image-grid">
      <article v-for="(item, index) in images" :key="item.id" class="image-card" :class="{ selected: selected.has(item.id) }" @click="openPreview(index)">
        <div class="image-frame" :style="{ aspectRatio: `${item.width || 4} / ${item.height || 3}` }"><img :src="item.url" :alt="item.original_name" loading="lazy"><UiCheckbox v-if="selectMode" class="select-check" :model-value="selected.has(item.id)" :aria-label="`选择 ${item.original_name}`" @click.stop @update:model-value="toggleSelected(item.id)" /></div>
        <div class="image-caption"><div><strong :title="item.original_name">{{ item.original_name }}</strong><span>{{ formatDate(item.created_at) }}</span></div><div><UiTooltip text="预览图片"><button aria-label="预览图片" @click.stop="openPreview(index)"><MoreHorizontal :size="18"/></button></UiTooltip><span>{{ formatSize(item.size) }}</span></div></div>
      </article>
    </div>
    <button v-if="cursor && !loading" class="load-more" :disabled="loadingMore" @click="load(false)"><LoaderCircle v-if="loadingMore" class="spin" :size="17"/>{{ loadingMore ? '正在加载…' : '加载更多' }}</button>
    <p v-else-if="images.length" class="gallery-end">已显示全部图片</p>

    <DialogRoot :open="Boolean(preview)" @update:open="!$event && closePreview()">
      <DialogPortal>
        <DialogOverlay class="lightbox-overlay" />
        <DialogContent v-if="preview" class="lightbox-panel">
          <UiTooltip text="关闭预览" side="left"><DialogClose as-child><button class="lightbox-close" aria-label="关闭预览"><X :size="24"/></button></DialogClose></UiTooltip>
          <div class="preview-stage">
            <img :src="preview.url" :alt="preview.original_name" :style="{ transform: `scale(${zoom})` }">
            <UiTooltip text="上一张" side="right"><button class="preview-nav prev" aria-label="上一张" @click="move(-1)"><ChevronLeft :size="26"/></button></UiTooltip><UiTooltip text="下一张" side="left"><button class="preview-nav next" aria-label="下一张" @click="move(1)"><ChevronRight :size="26"/></button></UiTooltip>
            <div class="zoom-controls"><UiTooltip text="缩小" side="top"><button aria-label="缩小" @click="zoom = Math.max(.5, zoom - .25)"><ZoomOut :size="18"/></button></UiTooltip><span>{{ Math.round(zoom * 100) }}%</span><UiTooltip text="放大" side="top"><button aria-label="放大" @click="zoom = Math.min(2.5, zoom + .25)"><ZoomIn :size="18"/></button></UiTooltip></div>
          </div>
          <div class="preview-info"><div><DialogTitle as-child><h2>{{ preview.original_name }}</h2></DialogTitle><DialogDescription as-child><p>{{ storages.find(s => s.id === preview.storage_id)?.name || preview.storage_type }} · {{ preview.width }} × {{ preview.height }} · {{ formatSize(preview.size) }}</p></DialogDescription><p>上传于 {{ formatDate(preview.created_at) }}</p></div>
            <div class="preview-actions"><button @click="copy(preview, 'url')"><Link2 :size="17"/>复制链接</button><button @click="copy(preview, 'markdown')"><FileCode2 :size="17"/>Markdown</button><button @click="copy(preview, 'html')"><Code2 :size="17"/>HTML</button><button class="danger" :disabled="deleting" @click="deleteConfirmOpen = true"><Trash2 :size="17"/>{{ deleting ? '删除中…' : '删除图片' }}</button></div>
          </div>
        </DialogContent>
      </DialogPortal>
    </DialogRoot>
    <ConfirmDialog v-if="preview" v-model:open="deleteConfirmOpen" title="删除这张图片？" :description="`“${preview.original_name}”及存储中的原文件都会被删除，此操作不可撤销。`" confirm-label="删除图片" :busy="deleting" @confirm="remove(preview)" />
  </section>
</template>
