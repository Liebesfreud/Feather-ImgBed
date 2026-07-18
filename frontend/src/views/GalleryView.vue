<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { Search, CalendarDays, CheckSquare, ImageOff, LoaderCircle, X, ChevronLeft, ChevronRight, ChevronDown, ZoomIn, ZoomOut, Link2, FileCode2, Code2, Trash2, UploadCloud, RefreshCw, Braces, Heart, Tags, Tag, FolderPlus, SlidersHorizontal } from '@lucide/vue'
import { useRoute, useRouter, type LocationQuery, type LocationQueryRaw } from 'vue-router'
import { DialogClose, DialogContent, DialogDescription, DialogOverlay, DialogPortal, DialogRoot, DialogTitle, DropdownMenuContent, DropdownMenuItem, DropdownMenuPortal, DropdownMenuRoot, DropdownMenuTrigger } from 'reka-ui'
import { api, deleteJSON, patchJSON, postJSON, putJSON } from '../api'
import { toast } from '../toast'
import type { Album, ImageItem, ImageVariant, StorageRecord, Tag as TagItem } from '../types'
import { formatImageLink, joinImageLinks, readCopyPreferences, type LinkFormat } from '../linkFormats'
import ConfirmDialog from '../components/ui/ConfirmDialog.vue'
import UiCheckbox from '../components/ui/UiCheckbox.vue'
import UiSelect from '../components/ui/UiSelect.vue'
import UiTooltip from '../components/ui/UiTooltip.vue'
import TagManagerDialog from '../components/TagManagerDialog.vue'
import TagPickerDialog from '../components/TagPickerDialog.vue'
import AlbumPickerDialog from '../components/AlbumPickerDialog.vue'
import ImageManagementNav from '../components/ImageManagementNav.vue'

const route = useRoute()
const router = useRouter()
const images = ref<ImageItem[]>([])
const storages = ref<StorageRecord[]>([])
const tags = ref<TagItem[]>([])
const albums = ref<Album[]>([])
const cursor = ref('')
const loading = ref(true)
const initialLoadFailed = ref(false)
const loadingMore = ref(false)
const failed = ref(false)
const search = ref('')
const storage = ref('')
const from = ref('')
const to = ref('')
const order = ref<'asc' | 'desc'>('desc')
const favoriteOnly = ref(false)
const tagFilter = ref('')
const selected = ref(new Set<string>())
const selectMode = ref(false)
const previewIndex = ref(-1)
const zoom = ref(1)
const deleting = ref(false)
const deleteConfirmOpen = ref(false)
const bulkTrashOpen = ref(false)
const bulkWorking = ref(false)
const tagManagerOpen = ref(false)
const tagPickerOpen = ref(false)
const tagPickerMode = ref<'single' | 'bulk'>('single')
const filterOpen = ref(false)
const previewTags = ref<TagItem[]>([])
const previewDetail = ref<ImageItem | null>(null)
const selectedVariant = ref('original')
const tagsBusy = ref(false)
const favoriteBusy = ref(new Set<string>())
const albumPickerOpen = ref(false)
const albumBusy = ref(false)
const syncingFromRoute = ref(false)
let searchTimer = 0
let requestSequence = 0
let detailSequence = 0

const preview = computed(() => images.value[previewIndex.value])
const variants = computed(() => previewDetail.value?.variants || [])
const selectedVariantItem = computed<ImageVariant | null>(() => variants.value.find((item) => item.id === selectedVariant.value) || null)
const previewURL = computed(() => selectedVariantItem.value?.url || preview.value?.url || '')
const previewLinkItem = computed(() => preview.value ? { ...preview.value, url: previewURL.value } : null)
const variantOptions = computed(() => [
  { label: '原图', value: 'original' },
  ...variants.value.map((item) => ({ label: variantLabel(item.kind), value: item.id })),
])
const selectedImages = computed(() => images.value.filter((item) => selected.value.has(item.id)))
const hasFilters = computed(() => Boolean(search.value || storage.value || from.value || to.value || favoriteOnly.value || tagFilter.value || order.value !== 'desc'))
const advancedFilterCount = computed(() => [storage.value, from.value, to.value, tagFilter.value, order.value !== 'desc'].filter(Boolean).length)
const storageFilterValue = computed({ get: () => storage.value || '__all__', set: (value: string) => { storage.value = value === '__all__' ? '' : value } })
const storageOptions = computed(() => [{ label: '全部存储', value: '__all__' }, ...storages.value.map((item) => ({ label: item.name, value: item.id }))])
const orderOptions = [
  { label: '最新上传', value: 'desc' },
  { label: '最早上传', value: 'asc' },
]
const tagFilterValue = computed({ get: () => tagFilter.value || '__all__', set: (value: string) => { tagFilter.value = value === '__all__' ? '' : value } })
const tagOptions = computed(() => [{ label: '全部标签', value: '__all__' }, ...tags.value.map((item) => ({ label: item.name, value: item.id }))])

async function load(reset = false) {
  const requestID = ++requestSequence
  if (reset) { loading.value = true; images.value = []; cursor.value = '' } else loadingMore.value = true
  failed.value = false
  try {
    const params = new URLSearchParams({ limit: '24' })
    if (!reset && cursor.value) params.set('cursor', cursor.value)
    if (search.value) params.set('search', search.value)
    if (storage.value) params.set('storage_id', storage.value)
    if (from.value) params.set('from', localDateBoundary(from.value, false))
    if (to.value) params.set('to', localDateBoundary(to.value, true))
    if (favoriteOnly.value) params.set('favorite', 'true')
    if (tagFilter.value) params.set('tag_id', tagFilter.value)
    params.set('order', order.value)
    const result = await api<{ items: ImageItem[]; next_cursor: string }>(`/images?${params}`)
    if (requestID !== requestSequence) return
    images.value = reset ? result.items : [...images.value, ...result.items]
    cursor.value = result.next_cursor
  } catch {
    if (requestID === requestSequence) failed.value = true
  } finally {
    if (requestID === requestSequence) { loading.value = false; loadingMore.value = false }
  }
}
function localDateBoundary(value: string, endOfDay: boolean) {
  const [year, month, day] = value.split('-').map(Number)
  const date = endOfDay
    ? new Date(year, month - 1, day, 23, 59, 59, 999)
    : new Date(year, month - 1, day, 0, 0, 0, 0)
  return date.toISOString()
}
function firstQueryValue(value: LocationQuery[string]) {
  return Array.isArray(value) ? value[0] || '' : value || ''
}
function filterQuery(): LocationQueryRaw {
  const query: LocationQueryRaw = { order: order.value }
  if (search.value) query.search = search.value
  if (storage.value) query.storage = storage.value
  if (from.value) query.from = from.value
  if (to.value) query.to = to.value
  if (favoriteOnly.value) query.favorite = 'true'
  if (tagFilter.value) query.tag = tagFilter.value
  return query
}
function querySignature(query: LocationQuery | LocationQueryRaw) {
  return new URLSearchParams(Object.entries(query).flatMap(([key, value]) => {
    const normalized = Array.isArray(value) ? value[0] : value
    return normalized == null ? [] : [[key, String(normalized)]]
  }).sort(([left], [right]) => left.localeCompare(right))).toString()
}
async function syncRoute() {
  if (syncingFromRoute.value) return
  const query = filterQuery()
  if (querySignature(route.query) === querySignature(query)) {
    selected.value = new Set()
    await load(true)
    return
  }
  await router.replace({ query })
}
async function applyRouteAndLoad() {
  syncingFromRoute.value = true
  search.value = firstQueryValue(route.query.search)
  storage.value = firstQueryValue(route.query.storage)
  from.value = firstQueryValue(route.query.from)
  to.value = firstQueryValue(route.query.to)
  favoriteOnly.value = firstQueryValue(route.query.favorite) === 'true'
  tagFilter.value = firstQueryValue(route.query.tag)
  order.value = firstQueryValue(route.query.order) === 'asc' ? 'asc' : 'desc'
  await nextTick()
  syncingFromRoute.value = false
  const normalized = filterQuery()
  if (querySignature(route.query) !== querySignature(normalized)) {
    await router.replace({ query: normalized })
    return
  }
  selected.value = new Set()
  await load(true)
}
function clearFilters() { void router.replace({ query: { order: 'desc' } }) }
function clearAdvancedFilters() {
  storage.value = ''
  from.value = ''
  to.value = ''
  tagFilter.value = ''
  order.value = 'desc'
}
function toggleSelected(id: string) { const next = new Set(selected.value); next.has(id) ? next.delete(id) : next.add(id); selected.value = next }
function selectLoaded() { selected.value = new Set(images.value.map((item) => item.id)) }
function clearSelection() { selected.value = new Set() }
function openPreview(index: number) { if (selectMode.value) { toggleSelected(images.value[index].id); return }; previewIndex.value = index; zoom.value = 1; void loadPreviewData() }
function closePreview() { deleteConfirmOpen.value = false; previewIndex.value = -1; previewDetail.value = null; previewTags.value = []; selectedVariant.value = 'original' }
function move(delta: number) { if (!images.value.length) return; previewIndex.value = (previewIndex.value + delta + images.value.length) % images.value.length; zoom.value = 1; void loadPreviewData() }
function formatSize(bytes: number) { return bytes >= 1024 * 1024 ? `${(bytes / 1024 / 1024).toFixed(2)} MB` : `${Math.max(1, bytes / 1024).toFixed(0)} KB` }
function formatDate(value: string) { return new Intl.DateTimeFormat('zh-CN', { year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' }).format(new Date(value)) }
function variantLabel(kind: string) {
  return ({ thumbnail: '缩略图', webp: 'WebP', watermarked: '水印版本', preview: '预览图', avif: 'AVIF', jpeg_preview: 'JPEG 预览' } as Record<string, string>)[kind] || kind
}
async function writeClipboard(value: string, successMessage: string) {
  try { await navigator.clipboard.writeText(value); toast(successMessage) }
  catch { toast('浏览器拒绝了剪贴板权限，请在地址栏中允许后重试', 'error') }
}
async function copy(item: ImageItem, kind: LinkFormat) {
  await writeClipboard(formatImageLink(item, kind), '已复制到剪贴板')
}
async function copyPreview(kind: LinkFormat) {
  if (previewLinkItem.value) await copy(previewLinkItem.value, kind)
}
async function copySelected(kind: LinkFormat) {
  if (!selectedImages.value.length) { toast('请先选择图片', 'error'); return }
  const preferences = readCopyPreferences()
  await writeClipboard(
    joinImageLinks(selectedImages.value, kind, preferences.separator),
    `已复制 ${selectedImages.value.length} 条${linkLabel(kind)}链接`,
  )
}
function linkLabel(kind: LinkFormat) {
  return ({ url: ' URL ', markdown: ' Markdown ', html: ' HTML ', bbcode: ' BBCode ' })[kind]
}
async function trashSelected() {
  if (!selected.value.size) return
  bulkWorking.value = true
  try {
    const ids = [...selected.value]
    const result = await postJSON<{ requested: number; affected: number; not_found: number }>('/images/bulk', { action: 'trash', ids })
    images.value = images.value.filter((image) => !selected.value.has(image.id))
    clearSelection()
    bulkTrashOpen.value = false
    toast(`已将 ${result.affected} 张图片移入回收站${result.not_found ? `，${result.not_found} 张未找到` : ''}`)
  } catch (error) {
    toast(error instanceof Error ? error.message : '批量操作失败', 'error')
  } finally { bulkWorking.value = false }
}
async function toggleFavorite(item: ImageItem) {
  if (favoriteBusy.value.has(item.id)) return
  favoriteBusy.value = new Set([...favoriteBusy.value, item.id])
  try {
    const updated = await patchJSON<ImageItem>(`/images/${item.id}`, { favorite: !item.favorite })
    const index = images.value.findIndex((image) => image.id === item.id)
    if (index >= 0) images.value[index] = updated
    toast(updated.favorite ? '已加入收藏' : '已取消收藏')
  } catch (error) { toast(error instanceof Error ? error.message : '更新收藏失败', 'error') }
  finally {
    const next = new Set(favoriteBusy.value); next.delete(item.id); favoriteBusy.value = next
  }
}
async function setSelectedFavorite(value: boolean) {
  if (!selected.value.size) return
  bulkWorking.value = true
  try {
    await postJSON('/images/bulk', { action: 'favorite', value, ids: [...selected.value] })
    images.value = images.value.map((item) => selected.value.has(item.id) ? { ...item, favorite: value } : item)
    toast(value ? '已批量收藏' : '已批量取消收藏')
  } catch (error) { toast(error instanceof Error ? error.message : '批量收藏失败', 'error') }
  finally { bulkWorking.value = false }
}
async function loadPreviewTags() {
  const item = preview.value
  previewTags.value = []
  if (!item) return
  try { previewTags.value = await api<TagItem[]>(`/images/${item.id}/tags`) }
  catch { /* 标签不是预览图片的阻断项。 */ }
}
async function loadPreviewData() {
  const item = preview.value
  const sequence = ++detailSequence
  previewTags.value = []
  previewDetail.value = null
  selectedVariant.value = 'original'
  if (!item) return
  const [detailResult, tagsResult] = await Promise.allSettled([
    api<ImageItem>(`/images/${item.id}`),
    api<TagItem[]>(`/images/${item.id}/tags`),
  ])
  if (sequence !== detailSequence || preview.value?.id !== item.id) return
  if (detailResult.status === 'fulfilled') previewDetail.value = detailResult.value
  if (tagsResult.status === 'fulfilled') previewTags.value = tagsResult.value
}
function openSingleTagPicker() { tagPickerMode.value = 'single'; tagPickerOpen.value = true }
function openBulkTagPicker() { tagPickerMode.value = 'bulk'; tagPickerOpen.value = true }
async function saveTags(payload: { action: 'replace' | 'add' | 'remove'; tagIds: string[] }) {
  tagsBusy.value = true
  try {
    if (tagPickerMode.value === 'single' && preview.value) {
      await putJSON(`/images/${preview.value.id}/tags`, { tag_ids: payload.tagIds })
      await loadPreviewTags()
    } else {
      await postJSON('/images/bulk/tags', { action: payload.action, ids: [...selected.value], tag_ids: payload.tagIds })
    }
    tagPickerOpen.value = false
    toast('图片标签已更新')
  } catch (error) { toast(error instanceof Error ? error.message : '更新标签失败', 'error') }
  finally { tagsBusy.value = false }
}
async function addSelectedToAlbum(albumID: string) {
  if (!selected.value.size) return
  albumBusy.value = true
  try {
    const result = await postJSON<{ requested: number; added: number }>(`/albums/${albumID}/images`, { ids: [...selected.value] })
    albumPickerOpen.value = false
    const album = albums.value.find((item) => item.id === albumID)
    if (album) album.image_count += result.added
    toast(`已向相册添加 ${result.added} 张图片`)
  } catch (error) { toast(error instanceof Error ? error.message : '添加到相册失败', 'error') }
  finally { albumBusy.value = false }
}
async function remove(item: ImageItem) {
  deleting.value = true
  try { await deleteJSON(`/images/${item.id}`); images.value = images.value.filter((image) => image.id !== item.id); closePreview(); toast('图片已移入回收站') }
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
watch(search, () => {
  if (syncingFromRoute.value) return
  clearTimeout(searchTimer)
  searchTimer = window.setTimeout(() => void syncRoute(), 350)
})
watch([storage, from, to, order, favoriteOnly, tagFilter], () => void syncRoute(), { flush: 'post' })
watch(() => route.fullPath, () => void applyRouteAndLoad())
async function initializeGallery() {
  initialLoadFailed.value = false
  try {
    ;[storages.value, tags.value, albums.value] = await Promise.all([api<StorageRecord[]>('/storages'), api<TagItem[]>('/tags'), api<Album[]>('/albums')])
    await applyRouteAndLoad()
  } catch (error) {
    initialLoadFailed.value = true
    loading.value = false
    toast(error instanceof Error ? error.message : '图库初始化失败', 'error')
  }
}
onMounted(() => {
  window.addEventListener('keydown', onKey)
  void initializeGallery()
})
onBeforeUnmount(() => {
  clearTimeout(searchTimer)
  requestSequence++
  window.removeEventListener('keydown', onKey)
})
</script>

<template>
  <section class="content-stack gallery-view">
    <ImageManagementNav />
    <div v-if="initialLoadFailed" class="gallery-state load-error-banner"><ImageOff :size="36"/><h2>图库暂时无法初始化</h2><button class="soft-button" @click="initializeGallery"><RefreshCw :size="17"/>重新加载</button></div>
    <header class="page-heading gallery-heading"><h1>图片管理</h1><span v-if="images.length">{{ images.length }} 张图片</span></header>
    <div class="gallery-toolbar">
      <label class="search-control"><Search :size="18"/><input v-model.trim="search" placeholder="搜索图片名称" aria-label="搜索图片名称"></label>
      <button class="filter-chip toolbar-chip" :class="{ active: favoriteOnly }" @click="favoriteOnly = !favoriteOnly"><Heart :size="16" :fill="favoriteOnly ? 'currentColor' : 'none'"/>仅看收藏</button>
      <button class="filter-chip toolbar-chip" :class="{ active: filterOpen || advancedFilterCount }" :aria-expanded="filterOpen" @click="filterOpen = !filterOpen"><SlidersHorizontal :size="16"/>筛选<span v-if="advancedFilterCount" class="filter-count">{{ advancedFilterCount }}</span><ChevronDown :size="15" :class="{ rotated: filterOpen }"/></button>
      <button class="filter-chip toolbar-chip" @click="tagManagerOpen = true"><Tags :size="16"/>标签管理</button>
      <button class="soft-button manage-button" :class="{ active: selectMode }" @click="selectMode = !selectMode; clearSelection()"><CheckSquare :size="17"/>{{ selectMode ? '完成管理' : '批量管理' }}</button>
    </div>
    <div v-if="filterOpen" class="advanced-filters">
      <div class="gallery-select"><UiSelect v-model="storageFilterValue" :options="storageOptions" aria-label="按存储筛选" /></div>
      <label class="date-control"><CalendarDays :size="17"/><input v-model="from" type="date" aria-label="起始日期" title="起始日期"></label>
      <label class="date-control"><CalendarDays :size="17"/><input v-model="to" type="date" aria-label="结束日期" title="结束日期"></label>
      <div class="gallery-select sort-select"><UiSelect v-model="order" :options="orderOptions" aria-label="上传时间排序" /></div>
      <div class="gallery-select tag-filter"><Tag :size="16"/><UiSelect v-model="tagFilterValue" :options="tagOptions" aria-label="按标签筛选"/></div>
      <button v-if="advancedFilterCount" class="filter-clear" @click="clearAdvancedFilters">清除筛选</button>
    </div>
    <div v-if="loading" class="gallery-state"><LoaderCircle class="spin" :size="28"/><p>正在整理图库…</p></div>
    <div v-else-if="failed" class="gallery-state"><ImageOff :size="38"/><h2>图库暂时无法加载</h2><p>筛选条件已为你保留，请稍后重试。</p><button class="soft-button" @click="load(true)"><RefreshCw :size="17"/>重新加载</button></div>
    <div v-else-if="!images.length" class="gallery-state empty-state"><div class="empty-art"><ImageOff :size="46"/></div><h2>{{ hasFilters ? '没有找到匹配的图片' : '图库还是空的' }}</h2><p>{{ hasFilters ? '试试更换关键词或调整筛选条件。' : '上传第一张图片，开始构建你的灵感空间。' }}</p><button v-if="hasFilters" class="soft-button" @click="clearFilters">清除筛选</button><button v-else class="primary-button" @click="router.push('/upload')"><UploadCloud :size="18"/>上传第一张图片</button></div>
    <div v-else class="image-grid">
      <article v-for="(item, index) in images" :key="item.id" class="image-card" :class="{ selected: selected.has(item.id) }" @click="openPreview(index)">
        <div class="image-frame" :style="{ aspectRatio: `${item.width || 4} / ${item.height || 3}` }"><img :src="item.thumbnail_url || item.url" :alt="item.original_name" :loading="index < 4 ? 'eager' : 'lazy'" :fetchpriority="index === 0 ? 'high' : 'auto'" decoding="async"><UiCheckbox v-if="selectMode" class="select-check" :model-value="selected.has(item.id)" :aria-label="`选择 ${item.original_name}`" @click.stop @update:model-value="toggleSelected(item.id)" /><button v-else class="favorite-card-button" :class="{ active: item.favorite }" :disabled="favoriteBusy.has(item.id)" :aria-label="item.favorite ? `取消收藏 ${item.original_name}` : `收藏 ${item.original_name}`" @click.stop="toggleFavorite(item)"><Heart :size="17" :fill="item.favorite ? 'currentColor' : 'none'"/></button></div>
        <div class="image-caption"><div><strong :title="item.original_name">{{ item.original_name }}</strong><span>{{ formatDate(item.created_at) }}</span></div><span>{{ formatSize(item.size) }}</span></div>
      </article>
    </div>
    <button v-if="cursor && !loading" class="load-more" :disabled="loadingMore" @click="load(false)"><LoaderCircle v-if="loadingMore" class="spin" :size="17"/>{{ loadingMore ? '正在加载…' : '加载更多' }}</button>
    <p v-else-if="images.length" class="gallery-end">已显示全部图片</p>

    <aside v-if="selectMode" class="batch-bar" aria-label="批量操作栏">
      <strong>已选择 {{ selected.size }} 张</strong>
      <span class="batch-spacer"></span>
      <button @click="selectLoaded">全选已加载</button>
      <button :disabled="!selected.size" @click="clearSelection">清空</button>
      <span class="batch-divider"></span>
      <DropdownMenuRoot>
        <DropdownMenuTrigger as-child><button :disabled="!selected.size"><Link2 :size="16"/>复制链接<ChevronDown :size="14"/></button></DropdownMenuTrigger>
        <DropdownMenuPortal><DropdownMenuContent class="gallery-menu" :side-offset="8" align="end">
          <DropdownMenuItem class="gallery-menu-item" @select="copySelected('url')"><Link2 :size="16"/>URL</DropdownMenuItem>
          <DropdownMenuItem class="gallery-menu-item" @select="copySelected('markdown')"><FileCode2 :size="16"/>Markdown</DropdownMenuItem>
          <DropdownMenuItem class="gallery-menu-item" @select="copySelected('html')"><Code2 :size="16"/>HTML</DropdownMenuItem>
          <DropdownMenuItem class="gallery-menu-item" @select="copySelected('bbcode')"><Braces :size="16"/>BBCode</DropdownMenuItem>
        </DropdownMenuContent></DropdownMenuPortal>
      </DropdownMenuRoot>
      <DropdownMenuRoot>
        <DropdownMenuTrigger as-child><button :disabled="!selected.size"><Tags :size="16"/>整理<ChevronDown :size="14"/></button></DropdownMenuTrigger>
        <DropdownMenuPortal><DropdownMenuContent class="gallery-menu" :side-offset="8" align="end">
          <DropdownMenuItem class="gallery-menu-item" :disabled="bulkWorking" @select="setSelectedFavorite(true)"><Heart :size="16"/>收藏</DropdownMenuItem>
          <DropdownMenuItem class="gallery-menu-item" :disabled="bulkWorking" @select="setSelectedFavorite(false)"><Heart :size="16"/>取消收藏</DropdownMenuItem>
          <DropdownMenuItem class="gallery-menu-item" :disabled="tagsBusy" @select="openBulkTagPicker"><Tags :size="16"/>标签</DropdownMenuItem>
          <DropdownMenuItem class="gallery-menu-item" :disabled="albumBusy" @select="albumPickerOpen = true"><FolderPlus :size="16"/>加入相册</DropdownMenuItem>
        </DropdownMenuContent></DropdownMenuPortal>
      </DropdownMenuRoot>
      <button class="danger" :disabled="!selected.size || bulkWorking" @click="bulkTrashOpen = true"><Trash2 :size="16"/>移入回收站</button>
    </aside>

    <DialogRoot :open="Boolean(preview)" @update:open="!$event && closePreview()">
      <DialogPortal>
        <DialogOverlay class="lightbox-overlay" />
        <DialogContent v-if="preview" class="lightbox-panel">
          <UiTooltip text="关闭预览" side="left"><DialogClose as-child><button class="lightbox-close" aria-label="关闭预览"><X :size="24"/></button></DialogClose></UiTooltip>
          <div class="preview-stage">
            <img :src="previewURL" :alt="preview.original_name" decoding="async" :style="{ transform: `scale(${zoom})` }">
            <UiTooltip text="上一张" side="right"><button class="preview-nav prev" aria-label="上一张" @click="move(-1)"><ChevronLeft :size="26"/></button></UiTooltip><UiTooltip text="下一张" side="left"><button class="preview-nav next" aria-label="下一张" @click="move(1)"><ChevronRight :size="26"/></button></UiTooltip>
            <div class="zoom-controls"><UiTooltip text="缩小" side="top"><button aria-label="缩小" @click="zoom = Math.max(.5, zoom - .25)"><ZoomOut :size="18"/></button></UiTooltip><span>{{ Math.round(zoom * 100) }}%</span><UiTooltip text="放大" side="top"><button aria-label="放大" @click="zoom = Math.min(2.5, zoom + .25)"><ZoomIn :size="18"/></button></UiTooltip></div>
          </div>
          <div class="preview-info"><div><DialogTitle as-child><h2>{{ preview.original_name }}</h2></DialogTitle><DialogDescription as-child><p>{{ storages.find(s => s.id === preview.storage_id)?.name || preview.storage_type }} · {{ selectedVariantItem?.width || preview.width }} × {{ selectedVariantItem?.height || preview.height }} · {{ formatSize(selectedVariantItem?.size || preview.size) }}</p></DialogDescription><p>上传于 {{ formatDate(preview.created_at) }}</p><div v-if="variants.length" class="variant-select"><span>链接版本</span><UiSelect v-model="selectedVariant" :options="variantOptions" aria-label="选择图片版本"/></div><div v-if="previewTags.length" class="tag-chips"><span v-for="item in previewTags" :key="item.id"><i :style="{ background: item.color }"></i>{{ item.name }}</span></div></div>
            <div class="preview-actions"><button :class="{ favorite: preview.favorite }" @click="toggleFavorite(preview)"><Heart :size="17" :fill="preview.favorite ? 'currentColor' : 'none'"/>{{ preview.favorite ? '取消收藏' : '收藏' }}</button><button @click="openSingleTagPicker"><Tags :size="17"/>标签</button><button @click="copyPreview('url')"><Link2 :size="17"/>复制链接</button><button @click="copyPreview('markdown')"><FileCode2 :size="17"/>Markdown</button><button @click="copyPreview('html')"><Code2 :size="17"/>HTML</button><button @click="copyPreview('bbcode')"><Braces :size="17"/>BBCode</button><button class="danger" :disabled="deleting" @click="deleteConfirmOpen = true"><Trash2 :size="17"/>{{ deleting ? '处理中…' : '移入回收站' }}</button></div>
          </div>
        </DialogContent>
      </DialogPortal>
    </DialogRoot>
    <ConfirmDialog v-if="preview" v-model:open="deleteConfirmOpen" title="移入回收站？" :description="`“${preview.original_name}”将从图库中隐藏，之后仍可在回收站恢复。`" confirm-label="移入回收站" :busy="deleting" @confirm="remove(preview)" />
    <ConfirmDialog v-model:open="bulkTrashOpen" title="批量移入回收站？" :description="`已选择的 ${selected.size} 张图片将从图库中隐藏，之后仍可恢复。`" confirm-label="移入回收站" :busy="bulkWorking" @confirm="trashSelected" />
    <TagManagerDialog v-model:open="tagManagerOpen" @changed="tags = $event"/>
    <TagPickerDialog v-model:open="tagPickerOpen" :tags="tags" :initial-selected="tagPickerMode === 'single' ? previewTags.map(item => item.id) : []" :title="tagPickerMode === 'single' ? '设置图片标签' : `批量设置 ${selected.size} 张图片的标签`" :bulk="tagPickerMode === 'bulk'" :busy="tagsBusy" @save="saveTags"/>
    <AlbumPickerDialog v-model:open="albumPickerOpen" :albums="albums" :count="selected.size" :busy="albumBusy" @save="addSelectedToAlbum"/>
  </section>
</template>
