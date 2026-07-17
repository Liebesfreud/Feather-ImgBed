<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { FolderHeart, ImageOff, LoaderCircle, Pencil, Plus, RefreshCw, Trash2, X } from '@lucide/vue'
import { useRouter } from 'vue-router'
import { DialogClose, DialogContent, DialogDescription, DialogOverlay, DialogPortal, DialogRoot, DialogTitle } from 'reka-ui'
import { api, deleteJSON, postJSON, putJSON } from '../api'
import { toast } from '../toast'
import type { Album } from '../types'
import ConfirmDialog from '../components/ui/ConfirmDialog.vue'

const router = useRouter()
const albums = ref<Album[]>([])
const loading = ref(true)
const failed = ref(false)
const formOpen = ref(false)
const saving = ref(false)
const editing = ref<Album | null>(null)
const form = reactive({ name: '', description: '' })
const deleteOpen = ref(false)
const deleteTarget = ref<Album | null>(null)

async function load() {
  loading.value = true
  failed.value = false
  try { albums.value = await api<Album[]>('/albums') }
  catch { failed.value = true }
  finally { loading.value = false }
}
function openForm(album?: Album) {
  editing.value = album || null
  form.name = album?.name || ''
  form.description = album?.description || ''
  formOpen.value = true
}
async function saveAlbum() {
  if (!form.name.trim()) return
  saving.value = true
  try {
    const payload = { name: form.name.trim(), description: form.description.trim(), cover_image_id: editing.value?.cover_image_id || '' }
    if (editing.value) await putJSON<Album>(`/albums/${editing.value.id}`, payload)
    else await postJSON<Album>('/albums', payload)
    formOpen.value = false
    await load()
    toast(editing.value ? '相册已更新' : '相册已创建')
  } catch (error) { toast(error instanceof Error ? error.message : '保存相册失败', 'error') }
  finally { saving.value = false }
}
function requestDelete(album: Album) { deleteTarget.value = album; deleteOpen.value = true }
async function deleteAlbum() {
  if (!deleteTarget.value) return
  saving.value = true
  try {
    await deleteJSON(`/albums/${deleteTarget.value.id}`)
    albums.value = albums.value.filter((album) => album.id !== deleteTarget.value?.id)
    deleteOpen.value = false
    toast('相册已删除，图片仍保留在图库中')
  } catch (error) { toast(error instanceof Error ? error.message : '删除相册失败', 'error') }
  finally { saving.value = false; deleteTarget.value = null }
}

onMounted(() => void load())
</script>

<template>
  <section class="content-stack albums-view">
    <header class="page-heading"><div><h1>相册</h1><p>按主题整理图片，相册删除后不会影响图库中的原图。</p></div><button class="primary-button" @click="openForm()"><Plus :size="18"/>新建相册</button></header>
    <div v-if="loading" class="gallery-state"><LoaderCircle class="spin" :size="28"/>正在整理相册…</div>
    <div v-else-if="failed" class="gallery-state"><ImageOff :size="38"/><h2>相册暂时无法加载</h2><button class="soft-button" @click="load"><RefreshCw :size="17"/>重新加载</button></div>
    <div v-else-if="!albums.length" class="gallery-state empty-state"><div class="empty-art"><FolderHeart :size="44"/></div><h2>还没有相册</h2><p>创建一个相册，再从图库批量加入图片。</p><button class="primary-button" @click="openForm()"><Plus :size="17"/>创建第一个相册</button></div>
    <div v-else class="album-grid">
      <article v-for="album in albums" :key="album.id" class="album-card" @click="router.push(`/albums/${album.id}`)">
        <div class="album-cover"><img v-if="album.cover_url" :src="album.cover_url" :alt="`${album.name}封面`"><FolderHeart v-else :size="42"/></div>
        <div class="album-card-copy"><div><strong>{{ album.name }}</strong><span>{{ album.image_count }} 张图片</span></div><p>{{ album.description || '还没有相册描述。' }}</p></div>
        <div class="album-card-actions"><button :aria-label="`编辑${album.name}`" @click.stop="openForm(album)"><Pencil :size="16"/></button><button class="danger" :aria-label="`删除${album.name}`" @click.stop="requestDelete(album)"><Trash2 :size="16"/></button></div>
      </article>
    </div>

    <DialogRoot v-model:open="formOpen"><DialogPortal><DialogOverlay class="dialog-overlay"/><DialogContent class="form-dialog album-form-dialog">
      <header><span class="form-dialog-icon"><FolderHeart :size="20"/></span><div><DialogTitle>{{ editing ? '编辑相册' : '新建相册' }}</DialogTitle><DialogDescription>填写名称和描述，之后可从图库加入图片。</DialogDescription></div><DialogClose aria-label="关闭"><X :size="20"/></DialogClose></header>
      <form class="dialog-form" @submit.prevent="saveAlbum"><label>相册名称<input v-model.trim="form.name" maxlength="100" required></label><label>描述<textarea v-model.trim="form.description" maxlength="1000" rows="4" placeholder="记录这个相册的主题"></textarea></label><footer><button type="button" class="soft-button" @click="formOpen = false">取消</button><button class="primary-button" :disabled="saving">{{ saving ? '保存中…' : '保存相册' }}</button></footer></form>
    </DialogContent></DialogPortal></DialogRoot>
    <ConfirmDialog v-model:open="deleteOpen" title="删除这个相册？" :description="`“${deleteTarget?.name || ''}”会被删除，但其中的图片仍保留在图库。`" confirm-label="删除相册" :busy="saving" @confirm="deleteAlbum"/>
  </section>
</template>
