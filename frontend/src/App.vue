<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { CloudUpload, Images, Settings, LogOut, Feather, UserRound, Moon, Sun } from '@lucide/vue'
import { DropdownMenuContent, DropdownMenuItem, DropdownMenuPortal, DropdownMenuRoot, DropdownMenuTrigger, TooltipProvider } from 'reka-ui'
import { useAuthStore } from './stores/auth'
import AuthScreen from './components/AuthScreen.vue'
import Dither from './components/backgrounds/Dither.vue'
import ToastHost from './components/ToastHost.vue'
import UiTooltip from './components/ui/UiTooltip.vue'

const auth = useAuthStore()
const route = useRoute()
const router = useRouter()
const menuOpen = ref(false)
const theme = ref<'light' | 'dark'>('dark')

const savedTheme = localStorage.getItem('feather-theme')
if (savedTheme === 'light' || savedTheme === 'dark') {
  theme.value = savedTheme
} else if (window.matchMedia('(prefers-color-scheme: light)').matches) {
  theme.value = 'light'
}
document.documentElement.classList.toggle('dark', theme.value === 'dark')
document.documentElement.classList.toggle('light', theme.value === 'light')

const nav = [
  { to: '/upload', label: '上传图片', icon: CloudUpload },
  { to: '/gallery', label: '图片管理', icon: Images },
  { to: '/settings', label: '系统设置', icon: Settings },
]
const showApp = computed(() => !auth.checking && auth.initialized && auth.authenticated)
const isDark = computed(() => theme.value === 'dark')
const ditherColor: [number, number, number] = [0.11, 0.68, 0.3]

function toggleTheme() {
  theme.value = isDark.value ? 'light' : 'dark'
  document.documentElement.classList.toggle('dark', isDark.value)
  document.documentElement.classList.toggle('light', !isDark.value)
  localStorage.setItem('feather-theme', theme.value)
}

function resetAuth() {
  auth.reset()
}

async function logout() {
  menuOpen.value = false
  await auth.logout()
  router.push('/upload')
}

onMounted(async () => {
  window.addEventListener('feather:unauthorized', resetAuth)
  await auth.check()
})

onUnmounted(() => window.removeEventListener('feather:unauthorized', resetAuth))
</script>

<template>
  <TooltipProvider :delay-duration="350">
  <Dither class="global-dither" aria-hidden="true" :wave-color="ditherColor" :wave-speed="0.015" :wave-frequency="2.4" :wave-amplitude="0.22" :color-num="3" :pixel-size="3" :enable-mouse-interaction="false" />
  <div v-if="auth.checking" class="app-loader" aria-label="正在加载">
    <Feather :size="38" /><span>正在展开轻羽…</span>
  </div>
  <template v-else-if="!showApp">
    <AuthScreen />
    <UiTooltip :text="isDark ? '切换到浅色模式' : '切换到深色模式'" side="left"><button class="auth-theme-button theme-button" type="button" :aria-label="isDark ? '切换到浅色模式' : '切换到深色模式'" @click="toggleTheme">
      <Sun v-if="isDark" :size="19" /><Moon v-else :size="19" />
    </button></UiTooltip>
  </template>
  <div v-else class="app-shell">
    <header class="topbar">
      <RouterLink class="brand" to="/upload" aria-label="轻羽图床首页">
        <span class="brand-mark"><Feather :size="23" /></span>
        <span>轻羽图床</span>
      </RouterLink>
      <nav class="main-nav" aria-label="主导航">
        <RouterLink v-for="item in nav" :key="item.to" :to="item.to" :class="{ active: route.path === item.to }" :aria-current="route.path === item.to ? 'page' : undefined" :title="item.label">
          <component :is="item.icon" :size="19" />
          <span>{{ item.label }}</span>
        </RouterLink>
      </nav>
      <div class="topbar-actions">
        <UiTooltip :text="isDark ? '切换到浅色模式' : '切换到深色模式'"><button class="topbar-control theme-button" type="button" :aria-label="isDark ? '切换到浅色模式' : '切换到深色模式'" @click="toggleTheme">
          <Sun v-if="isDark" :size="19" /><Moon v-else :size="19" />
        </button></UiTooltip>
        <DropdownMenuRoot v-model:open="menuOpen">
          <DropdownMenuTrigger as-child><button class="topbar-control account-button" aria-label="打开用户菜单" title="用户菜单"><UserRound :size="19" /></button></DropdownMenuTrigger>
          <DropdownMenuPortal><DropdownMenuContent class="account-menu" :side-offset="8" align="end">
            <DropdownMenuItem class="account-menu-item" @select="logout"><LogOut :size="17" />退出登录</DropdownMenuItem>
          </DropdownMenuContent></DropdownMenuPortal>
        </DropdownMenuRoot>
      </div>
    </header>
    <main class="page"><RouterView /></main>
  </div>
  <ToastHost />
  </TooltipProvider>
</template>
