<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { Check, Copy, Info, LoaderCircle, RefreshCw, Save, ShieldCheck, Shuffle } from '@lucide/vue'
import { api, putJSON } from '../api'
import { normalizeProcessingSettings } from '../processingSettings'
import { toast } from '../toast'
import type { Album, Settings, Tag } from '../types'
import ImageManagementNav from '../components/ImageManagementNav.vue'
import UiSelect from '../components/ui/UiSelect.vue'
import UiSwitch from '../components/ui/UiSwitch.vue'

const settings = ref<Settings | null>(null)
const albums = ref<Album[]>([])
const tags = ref<Tag[]>([])
const loading = ref(true)
const failed = ref(false)
const saving = ref(false)
const copied = ref(false)

const albumOptions = computed(() => {
  const options = [
    { label: '全部图片，不限相册', value: '' },
    ...albums.value.map((album) => ({ label: album.name, value: album.id })),
  ]
  const selected = settings.value?.random.album_id
  if (selected && !albums.value.some((album) => album.id === selected)) {
    options.push({ label: '原限定相册已删除，请重新选择', value: selected })
  }
  return options
})
const tagOptions = computed(() => {
  const options = [
    { label: '全部图片，不限标签', value: '' },
    ...tags.value.map((tag) => ({ label: tag.name, value: tag.id })),
  ]
  const selected = settings.value?.random.tag_id
  if (selected && !tags.value.some((tag) => tag.id === selected)) {
    options.push({ label: '原限定标签已删除，请重新选择', value: selected })
  }
  return options
})
const endpoint = computed(() => {
  const base = settings.value?.site_url?.replace(/\/$/, '') || window.location.origin
  return `${base}/api/v1/random`
})
const scopeDescription = computed(() => {
  if (!settings.value) return ''
  const albumID = settings.value.random.album_id
  const tagID = settings.value.random.tag_id
  const album = albums.value.find((item) => item.id === albumID)?.name
  const tag = tags.value.find((item) => item.id === tagID)?.name
  if ((albumID && !album) || (tagID && !tag)) return '原限定相册或标签已不存在，当前不会抽取任何图片，请重新选择。'
  if (album && tag) return `仅从“${album}”中带有“${tag}”标签的图片抽取。`
  if (album) return `仅从“${album}”相册中的图片抽取。`
  if (tag) return `仅从带有“${tag}”标签的图片抽取。`
  return '当前会从全部未删除图片中抽取。'
})

function normalizeSettings(value: Settings): Settings {
  return {
    ...value,
    random: {
      enabled: Boolean(value.random?.enabled),
      album_id: value.random?.album_id || '',
      tag_id: value.random?.tag_id || '',
    },
    processing: normalizeProcessingSettings(value.processing),
  }
}

async function load() {
  loading.value = true
  failed.value = false
  try {
    const [loadedSettings, loadedAlbums, loadedTags] = await Promise.all([
      api<Settings>('/settings'),
      api<Album[]>('/albums'),
      api<Tag[]>('/tags'),
    ])
    settings.value = normalizeSettings(loadedSettings)
    albums.value = loadedAlbums
    tags.value = loadedTags
  } catch (error) {
    failed.value = true
    toast(error instanceof Error ? error.message : '随机图设置读取失败', 'error')
  } finally {
    loading.value = false
  }
}

async function save() {
  if (!settings.value || saving.value) return
  saving.value = true
  try {
    const random = { ...settings.value.random }
    const latest = normalizeSettings(await api<Settings>('/settings'))
    settings.value = normalizeSettings(await putJSON<Settings>('/settings', { ...latest, random }))
    toast('随机图设置已保存')
  } catch (error) {
    toast(error instanceof Error ? error.message : '随机图设置保存失败', 'error')
  } finally {
    saving.value = false
  }
}

async function copyEndpoint() {
  try {
    await navigator.clipboard.writeText(endpoint.value)
    copied.value = true
    window.setTimeout(() => { copied.value = false }, 1600)
    toast('随机图地址已复制')
  } catch {
    toast('复制失败，请手动复制地址', 'error')
  }
}

onMounted(() => void load())
</script>

<template>
  <section class="content-stack random-view">
    <ImageManagementNav />
    <header class="page-heading random-heading">
      <div><h1>随机图</h1><p>单独控制公开随机图 API，只让你选定范围内的图片参与抽取。</p></div>
      <button v-if="settings" class="primary-button" :disabled="saving" @click="save"><LoaderCircle v-if="saving" class="spin" :size="18"/><Save v-else :size="18"/>{{ saving ? '保存中…' : '保存设置' }}</button>
    </header>

    <div v-if="loading" class="gallery-state"><LoaderCircle class="spin" :size="28"/>正在读取随机图设置…</div>
    <div v-else-if="failed" class="gallery-state"><Info :size="38"/><h2>随机图设置暂时无法加载</h2><button class="soft-button" @click="load"><RefreshCw :size="17"/>重新加载</button></div>

    <template v-else-if="settings">
      <section class="random-overview" :class="{ enabled: settings.random.enabled }">
        <span class="random-overview-icon"><Shuffle :size="25"/></span>
        <div class="random-overview-copy">
          <span class="random-status">{{ settings.random.enabled ? '公开接口已开启' : '公开接口已关闭' }}</span>
          <h2>{{ settings.random.enabled ? '随机图可以被外部访问' : '私人图片不会通过随机图公开' }}</h2>
          <p>{{ settings.random.enabled ? scopeDescription : '关闭时 /api/v1/random 返回 404，可先配置范围再启用。' }}</p>
        </div>
        <UiSwitch v-model="settings.random.enabled" aria-label="启用公开随机图 API" />
      </section>

      <div v-if="settings.random.enabled && !settings.random.album_id && !settings.random.tag_id" class="random-warning">
        <ShieldCheck :size="18"/><span><strong>当前没有范围限制</strong>全部未删除图片都有机会被公开抽取。</span>
      </div>

      <div class="random-layout">
        <section class="form-section random-scope-card">
          <h2>图片范围</h2>
          <div class="form-grid">
            <label>限定相册<UiSelect v-model="settings.random.album_id" :options="albumOptions" aria-label="随机图限定相册" /></label>
            <label>限定标签<UiSelect v-model="settings.random.tag_id" :options="tagOptions" aria-label="随机图限定标签" /></label>
          </div>
          <p class="random-scope-summary">{{ scopeDescription }} 同时选择相册和标签时取两者交集。</p>
        </section>

        <section class="form-section random-endpoint-card">
          <h2>调用地址</h2>
          <p>可直接作为网页图片地址使用；添加 <code>?format=json</code> 可获取图片信息。</p>
          <div class="random-endpoint">
            <code>{{ endpoint }}</code>
            <button class="soft-button" type="button" @click="copyEndpoint"><Check v-if="copied" :size="17"/><Copy v-else :size="17"/>{{ copied ? '已复制' : '复制地址' }}</button>
          </div>
        </section>
      </div>
    </template>
  </section>
</template>
