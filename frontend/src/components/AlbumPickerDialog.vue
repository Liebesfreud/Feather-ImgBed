<script setup lang="ts">
import { ref, watch } from 'vue'
import { FolderPlus, X } from '@lucide/vue'
import { DialogClose, DialogContent, DialogDescription, DialogOverlay, DialogPortal, DialogRoot, DialogTitle } from 'reka-ui'
import type { Album } from '../types'
import UiSelect from './ui/UiSelect.vue'

const props = defineProps<{ open: boolean; albums: Album[]; busy?: boolean; count: number }>()
const emit = defineEmits<{ 'update:open': [value: boolean]; save: [albumID: string] }>()
const albumID = ref('')
const options = () => props.albums.map((album) => ({ label: `${album.name}（${album.image_count} 张）`, value: album.id }))

watch(() => props.open, (open) => { if (open) albumID.value = props.albums[0]?.id || '' })
</script>

<template>
  <DialogRoot :open="open" @update:open="emit('update:open', $event)">
    <DialogPortal>
      <DialogOverlay class="dialog-overlay" />
      <DialogContent class="form-dialog album-picker-dialog">
        <header><span class="form-dialog-icon"><FolderPlus :size="20"/></span><div><DialogTitle>添加到相册</DialogTitle><DialogDescription>把已选择的 {{ count }} 张图片加入一个相册，已存在的图片会自动跳过。</DialogDescription></div><DialogClose aria-label="关闭"><X :size="20"/></DialogClose></header>
        <UiSelect v-if="albums.length" v-model="albumID" :options="options()" aria-label="目标相册"/>
        <p v-else class="section-note album-picker-empty">还没有相册，请先到“相册”页面创建。</p>
        <footer><button class="soft-button" @click="emit('update:open', false)">取消</button><button class="primary-button" :disabled="busy || !albumID" @click="emit('save', albumID)">{{ busy ? '添加中…' : '添加图片' }}</button></footer>
      </DialogContent>
    </DialogPortal>
  </DialogRoot>
</template>
