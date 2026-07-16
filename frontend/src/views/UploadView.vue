<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { CloudUpload, ImagePlus, Clipboard, CheckCircle2, CircleAlert, LoaderCircle, RotateCcw, Link2, Code2, FileCode2, Copy, Trash2, Database } from '@lucide/vue'
import { api } from '../api'
import { toast } from '../toast'
import type { ImageItem, Settings, StorageRecord } from '../types'
import UiSelect from '../components/ui/UiSelect.vue'

interface QueueItem {
  id: string
  file: File
  preview: string
  state: 'waiting' | 'uploading' | 'success' | 'error'
  progress: number
  result?: ImageItem
  error?: string
}

const input = ref<HTMLInputElement>()
const queue = ref<QueueItem[]>([])
const dragging = ref(false)
const selectedStorage = ref('')
const storages = ref<StorageRecord[]>([])
const settings = ref<Settings | null>(null)
const uploading = computed(() => queue.value.some((item) => item.state === 'uploading'))
const successes = computed(() => queue.value.filter((item) => item.state === 'success'))
const storageOptions = computed(() => storages.value.filter((item) => item.enabled).map((item) => ({ label: item.name, value: item.id })))

function addFiles(files: File[]) {
  const max = settings.value?.max_batch_count || 10
  const allowed = settings.value?.allowed_types || ['image/jpeg', 'image/png', 'image/gif', 'image/webp']
  const size = settings.value?.max_file_size || 20 * 1024 * 1024
  const remaining = max - queue.value.filter((item) => item.state !== 'success').length
  files.slice(0, Math.max(0, remaining)).forEach((file) => {
    if (!allowed.includes(file.type)) { toast(`${file.name} 不是支持的图片格式`, 'error'); return }
    if (file.size > size) { toast(`${file.name} 超过 ${formatSize(size)} 限制`, 'error'); return }
    queue.value.push({ id: `${Date.now()}-${Math.random()}`, file, preview: URL.createObjectURL(file), state: 'waiting', progress: 0 })
  })
  if (files.length > remaining) toast(`单批最多上传 ${max} 张图片`, 'error')
  void uploadWaiting()
}

async function uploadWaiting() {
  for (const item of queue.value.filter((value) => value.state === 'waiting')) await uploadOne(item)
}

async function uploadOne(item: QueueItem) {
  item.state = 'uploading'; item.progress = 15; item.error = undefined
  const data = new FormData(); data.append('file', item.file)
  if (selectedStorage.value) data.append('storage_id', selectedStorage.value)
  const timer = window.setInterval(() => { if (item.progress < 88) item.progress += Math.max(1, Math.round((88 - item.progress) / 8)) }, 160)
  try {
    item.result = await api<ImageItem>('/images', { method: 'POST', body: data })
    item.progress = 100; item.state = 'success'; toast(`${item.file.name} 上传成功`)
  } catch (error) {
    item.state = 'error'; item.error = error instanceof Error ? error.message : '上传失败，请重试'
  } finally { clearInterval(timer) }
}

function onDrop(event: DragEvent) { dragging.value = false; addFiles(Array.from(event.dataTransfer?.files || [])) }
function onPick(event: Event) { addFiles(Array.from((event.target as HTMLInputElement).files || [])); (event.target as HTMLInputElement).value = '' }
function onPaste(event: ClipboardEvent) {
  const files = Array.from(event.clipboardData?.files || []).filter((file) => file.type.startsWith('image/'))
  if (files.length) { event.preventDefault(); addFiles(files) }
}
function formatSize(bytes: number) {
  if (bytes < 1024 * 1024) return `${Math.max(1, bytes / 1024).toFixed(0)} KB`
  const megabytes = bytes / 1024 / 1024
  return `${Number.isInteger(megabytes) ? megabytes : megabytes.toFixed(2)} MB`
}
function linkText(image: ImageItem, format: 'url' | 'markdown' | 'html') {
  if (format === 'markdown') return `![${image.original_name}](${image.url})`
  if (format === 'html') return `<img src="${image.url}" alt="${image.original_name}">`
  return image.url
}
async function copy(image: ImageItem, format: 'url' | 'markdown' | 'html') { await navigator.clipboard.writeText(linkText(image, format)); toast('已复制到剪贴板') }
async function copyAll() { await navigator.clipboard.writeText(successes.value.map((item) => linkText(item.result!, 'markdown')).join('\n')); toast(`已复制 ${successes.value.length} 条 Markdown 链接`) }
function clearCompleted() { queue.value.filter((item) => item.state === 'success').forEach((item) => URL.revokeObjectURL(item.preview)); queue.value = queue.value.filter((item) => item.state !== 'success') }
function beforeUnload(event: BeforeUnloadEvent) { if (uploading.value) event.preventDefault() }

onMounted(async () => {
  document.addEventListener('paste', onPaste)
  window.addEventListener('beforeunload', beforeUnload)
  ;[storages.value, settings.value] = await Promise.all([api<StorageRecord[]>('/storages'), api<Settings>('/settings')])
  selectedStorage.value = settings.value.default_storage_id
})
onBeforeUnmount(() => { document.removeEventListener('paste', onPaste); window.removeEventListener('beforeunload', beforeUnload); queue.value.forEach((item) => URL.revokeObjectURL(item.preview)) })
</script>

<template>
  <section class="content-stack upload-view">
    <header class="page-heading"><div><h1>上传图片</h1><p>拖放、粘贴或选择文件，链接会在上传完成后立即生成。</p></div>
      <div class="storage-select"><Database :size="17"/><span>上传至</span><UiSelect v-model="selectedStorage" :options="storageOptions" aria-label="选择上传存储" /></div>
    </header>
    <div class="dropzone" :class="{ dragging }" @dragenter.prevent="dragging = true" @dragover.prevent @dragleave.prevent="dragging = false" @drop.prevent="onDrop" @click="input?.click()">
      <input ref="input" class="sr-only" type="file" accept="image/jpeg,image/png,image/gif,image/webp" multiple @change="onPick">
      <div class="upload-art"><CloudUpload :size="42"/></div>
      <h2>拖放图片到此处，或选择本地文件</h2>
      <p>支持 JPEG、PNG、GIF、WebP · 单文件最大 {{ formatSize(settings?.max_file_size || 20 * 1024 * 1024) }}</p>
      <button class="primary-button" type="button"><ImagePlus :size="19"/>选择图片</button>
      <small><Clipboard :size="15"/>也可以按 Ctrl / Cmd + V 粘贴图片</small>
    </div>
    <section v-if="queue.length" class="queue-panel">
      <header class="queue-head"><div><h2>上传队列 <span>({{ queue.length }})</span></h2><button v-if="successes.length" class="soft-button" @click="copyAll"><Copy :size="16"/>全部复制</button></div><div><span>完成 {{ successes.length }} / {{ queue.length }}</span><button v-if="successes.length" class="text-button danger" @click="clearCompleted"><Trash2 :size="16"/>清空已完成</button></div></header>
      <div class="queue-list">
        <article v-for="item in queue" :key="item.id" class="queue-row">
          <img :src="item.preview" :alt="item.file.name"><div class="file-meta"><strong>{{ item.file.name }}</strong><span>{{ item.file.type }} · {{ formatSize(item.file.size) }}</span></div>
          <div class="upload-status" :class="item.state">
            <span><CheckCircle2 v-if="item.state === 'success'" :size="17"/><CircleAlert v-else-if="item.state === 'error'" :size="17"/><LoaderCircle v-else class="spin" :size="17"/>{{ item.state === 'success' ? '上传成功' : item.state === 'error' ? item.error : item.state === 'waiting' ? '等待上传' : '正在上传' }}</span>
            <div class="progress"><i :style="{ width: `${item.progress}%` }"></i></div>
          </div>
          <span class="storage-tag">{{ storages.find(s => s.id === (item.result?.storage_id || selectedStorage))?.name || '默认存储' }}</span>
          <div class="row-actions">
            <template v-if="item.result"><button @click="copy(item.result, 'url')"><Link2 :size="16"/><span>复制链接</span></button><button @click="copy(item.result, 'markdown')"><FileCode2 :size="16"/><span>Markdown</span></button><button @click="copy(item.result, 'html')"><Code2 :size="16"/><span>HTML</span></button></template>
            <button v-else-if="item.state === 'error'" class="danger" @click="uploadOne(item)"><RotateCcw :size="16"/>重试</button>
          </div>
        </article>
      </div>
    </section>
    <div v-else class="gentle-tip"><span>小提示</span>一次可上传 {{ settings?.max_batch_count || 10 }} 张图片，每张图片都会独立处理，单项失败不会影响其他文件。</div>
  </section>
</template>
