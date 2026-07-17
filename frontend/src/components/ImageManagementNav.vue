<script setup lang="ts">
import { FolderHeart, Images, Trash2 } from '@lucide/vue'
import { useRoute } from 'vue-router'

const route = useRoute()
const items = [
  { to: '/gallery', label: '全部图片', icon: Images, active: () => route.path === '/gallery' },
  { to: '/albums', label: '相册', icon: FolderHeart, active: () => route.path.startsWith('/albums') },
  { to: '/trash', label: '回收站', icon: Trash2, active: () => route.path === '/trash' },
]
</script>

<template>
  <nav class="management-nav" aria-label="图片管理分类">
    <RouterLink
      v-for="item in items"
      :key="item.to"
      :to="item.to"
      :class="{ active: item.active() }"
      :aria-current="item.active() ? 'page' : undefined"
    >
      <component :is="item.icon" :size="17" />
      <span>{{ item.label }}</span>
    </RouterLink>
  </nav>
</template>
