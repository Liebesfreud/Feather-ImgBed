<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ArrowLeft, Check, ChevronLeft, ChevronRight, Copy, FolderHeart, ImageOff, LoaderCircle, Pencil, RefreshCw, Trash2, X } from '@lucide/vue'
import { useRoute, useRouter } from 'vue-router'
import { DialogClose, DialogContent, DialogOverlay, DialogPortal, DialogRoot, DialogTitle } from 'reka-ui'
import { api, deleteJSON, putJSON } from '../api'
import { toast } from '../toast'
import { formatImageLink, readCopyPreferences } from '../linkFormats'
import type { Album, ImageItem } from '../types'
import ConfirmDialog from '../components/ui/ConfirmDialog.vue'
import ImageManagementNav from '../components/ImageManagementNav.vue'

const route = useRoute()
const router = useRouter()
const album = ref<Album | null>(null)
const images = ref<ImageItem[]>([])
const loading = ref(true)
const failed = ref(false)
const working = ref(false)
const previewIndex = ref(-1)
const editOpen = ref(false)
const dangerOpen = ref(false)
const dangerAction = ref<{ kind: 'remove'; item: ImageItem } | { kind: 'album' } | null>(null)
const form = reactive({ name: '', description: '' })

const preview = computed(() => images.value[previewIndex.value])
const dangerTitle = computed(() => dangerAction.value?.kind === 'album' ? '删除这个相册？' : '从相册移除图片？')
const dangerDescription = computed(() => dangerAction.value?.kind === 'album'
  ? `“${album.value?.name || ''}”会被删除，其中的图片仍保留在图库。`
  : `“${dangerAction.value?.kind === 'remove' ? dangerAction.value.item.original_name : ''}”只会从当前相册移除。`)

async function load() {
  loading.value = true
  failed.value = false
  try {
    const result = await api<{ album: Album; images: ImageItem[] }>(`/albums/${route.params.id}`)
    album.value = result.album
    images.value = result.images
  } catch { failed.value = true }
  finally { loading.value = false }
}
function openEdit() {
  if (!album.value) return
  form.name = album.value.name
  form.description = album.value.description
  editOpen.value = true
}
async function saveAlbum(coverImageID = album.value?.cover_image_id || '') {
  if (!album.value || !form.name.trim() && editOpen.value) return
  working.value = true
  try {
    album.value = await putJSON<Album>(`/albums/${album.value.id}`, {
      name: editOpen.value ? form.name.trim() : album.value.name,
      description: editOpen.value ? form.description.trim() : album.value.description,
      cover_image_id: coverImageID,
    })
    editOpen.value = false
    toast(coverImageID ? '相册封面已更新' : '相册已更新')
  } catch (error) { toast(error instanceof Error ? error.message : '更新相册失败', 'error') }
  finally { working.value = false }
}
function requestDanger(action: NonNullable<typeof dangerAction.value>) { dangerAction.value = action; dangerOpen.value = true }
async function confirmDanger() {
  if (!album.value || !dangerAction.value) return
  working.value = true
  try {
    if (dangerAction.value.kind === 'album') {
      await deleteJSON(`/albums/${album.value.id}`)
      await router.push('/albums')
      toast('相册已删除，图片仍保留在图库中')
    } else {
      const item = dangerAction.value.item
      await deleteJSON(`/albums/${album.value.id}/images/${item.id}`)
      images.value = images.value.filter((image) => image.id !== item.id)
      if (album.value.cover_image_id === item.id) album.value = { ...album.value, cover_image_id: '', cover_url: '' }
      previewIndex.value = -1
      toast('图片已移出相册')
    }
    dangerOpen.value = false
  } catch (error) { toast(error instanceof Error ? error.message : '操作失败', 'error') }
  finally { working.value = false; dangerAction.value = null }
}
function move(delta: number) {
  if (!images.value.length) return
  previewIndex.value = (previewIndex.value + delta + images.value.length) % images.value.length
}
async function copyPreview() {
  if (!preview.value) return
  const preferences = readCopyPreferences()
  try { await navigator.clipboard.writeText(formatImageLink(preview.value, preferences.format)); toast('链接已复制') }
  catch { toast('浏览器拒绝了剪贴板权限', 'error') }
}

onMounted(() => void load())
</script>

<template>
  <section class="content-stack album-detail-view">
    <ImageManagementNav />
    <button class="back-link" @click="router.push('/albums')"><ArrowLeft :size="17"/>返回相册</button>
    <header v-if="album" class="page-heading album-detail-heading"><div><h1>{{ album.name }}</h1><p><template v-if="album.description">{{ album.description }} · </template>{{ images.length }} 张图片</p></div><div><button class="soft-button" @click="openEdit"><Pencil :size="17"/>编辑</button><button class="soft-button danger" @click="requestDanger({ kind: 'album' })"><Trash2 :size="17"/>删除相册</button></div></header>
    <div v-if="loading" class="gallery-state"><LoaderCircle class="spin" :size="28"/>正在打开相册…</div>
    <div v-else-if="failed" class="gallery-state"><ImageOff :size="38"/><h2>相册暂时无法加载</h2><button class="soft-button" @click="load"><RefreshCw :size="17"/>重新加载</button></div>
    <div v-else-if="!images.length" class="gallery-state empty-state"><div class="empty-art"><FolderHeart :size="44"/></div><h2>相册还是空的</h2><button class="primary-button" @click="router.push('/gallery')">前往图库</button></div>
    <div v-else class="image-grid album-image-grid">
      <article v-for="(item, index) in images" :key="item.id" class="image-card album-image-card" @click="previewIndex = index">
        <div class="image-frame" :style="{ aspectRatio: `${item.width || 4} / ${item.height || 3}` }"><img :src="item.thumbnail_url || item.url" :alt="item.original_name" :loading="index < 4 ? 'eager' : 'lazy'" :fetchpriority="index === 0 ? 'high' : 'auto'" decoding="async"><span v-if="album?.cover_image_id === item.id" class="cover-label"><Check :size="13"/>封面</span></div>
        <div class="album-image-copy"><strong>{{ item.original_name }}</strong><div><button :disabled="working" @click.stop="saveAlbum(item.id)"><FolderHeart :size="15"/>设为封面</button><button class="danger" :disabled="working" @click.stop="requestDanger({ kind: 'remove', item })"><Trash2 :size="15"/>移出</button></div></div>
      </article>
    </div>

    <DialogRoot :open="Boolean(preview)" @update:open="!$event && (previewIndex = -1)"><DialogPortal><DialogOverlay class="lightbox-overlay"/><DialogContent v-if="preview" class="lightbox-panel album-lightbox" :aria-describedby="undefined">
      <DialogClose class="lightbox-close" aria-label="关闭预览"><X :size="24"/></DialogClose>
      <div class="preview-stage"><img :src="preview.url" :alt="preview.original_name" decoding="async"><button class="preview-nav prev" aria-label="上一张" @click="move(-1)"><ChevronLeft :size="26"/></button><button class="preview-nav next" aria-label="下一张" @click="move(1)"><ChevronRight :size="26"/></button></div>
      <div class="preview-info"><div><DialogTitle as-child><h2>{{ preview.original_name }}</h2></DialogTitle></div><div class="preview-actions"><button @click="copyPreview"><Copy :size="17"/>复制链接</button><button @click="saveAlbum(preview.id)"><FolderHeart :size="17"/>设为封面</button><button class="danger" @click="requestDanger({ kind: 'remove', item: preview })"><Trash2 :size="17"/>移出相册</button></div></div>
    </DialogContent></DialogPortal></DialogRoot>

    <DialogRoot v-model:open="editOpen"><DialogPortal><DialogOverlay class="dialog-overlay"/><DialogContent class="form-dialog album-form-dialog" :aria-describedby="undefined">
      <header><span class="form-dialog-icon"><FolderHeart :size="20"/></span><div><DialogTitle>编辑相册</DialogTitle></div><DialogClose aria-label="关闭"><X :size="20"/></DialogClose></header>
      <form class="dialog-form" @submit.prevent="saveAlbum()"><label>相册名称<input v-model.trim="form.name" maxlength="100" required></label><label>描述<textarea v-model.trim="form.description" maxlength="1000" rows="4"></textarea></label><footer><button type="button" class="soft-button" @click="editOpen = false">取消</button><button class="primary-button" :disabled="working">{{ working ? '保存中…' : '保存修改' }}</button></footer></form>
    </DialogContent></DialogPortal></DialogRoot>
    <ConfirmDialog v-model:open="dangerOpen" :title="dangerTitle" :description="dangerDescription" :confirm-label="dangerAction?.kind === 'album' ? '删除相册' : '移出相册'" :busy="working" @confirm="confirmDanger"/>
  </section>
</template>
