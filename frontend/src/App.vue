<script setup lang="ts">
import { computed, defineAsyncComponent, nextTick, onMounted, onUnmounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { CloudUpload, Images, Settings, LogOut, Feather, UserRound, Moon, Sun, Braces } from '@lucide/vue'
import { useAuthStore } from './stores/auth'
import AuthScreen from './components/AuthScreen.vue'
import UiTooltip from './components/ui/UiTooltip.vue'

const Dither = defineAsyncComponent(() => import('./components/backgrounds/Dither.vue'))
const ToastHost = defineAsyncComponent(() => import('./components/ToastHost.vue'))

const auth = useAuthStore()
const route = useRoute()
const router = useRouter()
const menuOpen = ref(false)
const accountMenuRoot = ref<HTMLElement>()
const accountMenuButton = ref<HTMLButtonElement>()
const accountMenuItem = ref<HTMLButtonElement>()
const theme = ref<'light' | 'dark'>('dark')
const showDither = ref(false)
const showToastHost = ref(false)
let ditherDelay: number | null = null
let ditherIdleRequest: number | null = null
let toastDelay: number | null = null

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
  { to: '/developer', label: 'API', icon: Braces },
  { to: '/settings', label: '系统设置', icon: Settings },
]
const managementPaths = ['/gallery', '/albums', '/trash']
const showApp = computed(() => !auth.checking && auth.initialized && auth.authenticated)
const isDark = computed(() => theme.value === 'dark')
const ditherColor: [number, number, number] = [0.11, 0.68, 0.3]

function isNavActive(path: string) {
  if (path === '/gallery') {
    return managementPaths.some(item => route.path === item || route.path.startsWith(`${item}/`))
  }
  return route.path === path
}

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

function toggleAccountMenu() {
  menuOpen.value = !menuOpen.value
  if (menuOpen.value) void nextTick(() => accountMenuItem.value?.focus())
}

function closeAccountMenuOnOutsideClick(event: PointerEvent) {
  if (menuOpen.value && !accountMenuRoot.value?.contains(event.target as Node)) menuOpen.value = false
}

function closeAccountMenuOnEscape(event: KeyboardEvent) {
  if (event.key !== 'Escape' || !menuOpen.value) return
  menuOpen.value = false
  accountMenuButton.value?.focus()
}

function closeAccountMenuOnFocusOut(event: FocusEvent) {
  const next = event.relatedTarget as Node | null
  if (!next || !accountMenuRoot.value?.contains(next)) menuOpen.value = false
}

function scheduleDither() {
  if (window.matchMedia('(prefers-reduced-motion: reduce)').matches) return
  ditherDelay = window.setTimeout(() => {
    ditherDelay = null
    if ('requestIdleCallback' in window) {
      ditherIdleRequest = window.requestIdleCallback(() => {
        ditherIdleRequest = null
        showDither.value = true
      }, { timeout: 1600 })
    } else {
      showDither.value = true
    }
  }, 900)
}

async function initializeApp() {
  try {
    await auth.check()
  } catch {
    // The auth store leaves checking state consistently; the existing screen
    // can still present a retryable authentication flow.
  } finally {
    toastDelay = window.setTimeout(() => {
      toastDelay = null
      showToastHost.value = true
    }, 300)
    scheduleDither()
  }
}

void initializeApp()

onMounted(() => {
  window.addEventListener('feather:unauthorized', resetAuth)
  document.addEventListener('pointerdown', closeAccountMenuOnOutsideClick)
  document.addEventListener('keydown', closeAccountMenuOnEscape)
})

onUnmounted(() => {
  window.removeEventListener('feather:unauthorized', resetAuth)
  document.removeEventListener('pointerdown', closeAccountMenuOnOutsideClick)
  document.removeEventListener('keydown', closeAccountMenuOnEscape)
  if (ditherDelay !== null) window.clearTimeout(ditherDelay)
  if (ditherIdleRequest !== null) window.cancelIdleCallback(ditherIdleRequest)
  if (toastDelay !== null) window.clearTimeout(toastDelay)
})
</script>

<template>
  <div v-if="!auth.checking" class="global-dither" aria-hidden="true">
    <Dither v-if="showDither" :wave-color="ditherColor" :wave-speed="0.015" :wave-frequency="2.4" :wave-amplitude="0.22" :color-num="3" :pixel-size="1.5" :render-scale="0.5" :max-fps="24" :enable-mouse-interaction="false" />
  </div>
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
        <RouterLink v-for="item in nav" :key="item.to" :to="item.to" :class="{ active: isNavActive(item.to) }" :aria-current="isNavActive(item.to) ? 'page' : undefined" :title="item.label">
          <component :is="item.icon" :size="19" />
          <span>{{ item.label }}</span>
        </RouterLink>
      </nav>
      <div class="topbar-actions">
        <UiTooltip :text="isDark ? '切换到浅色模式' : '切换到深色模式'"><button class="topbar-control theme-button" type="button" :aria-label="isDark ? '切换到浅色模式' : '切换到深色模式'" @click="toggleTheme">
          <Sun v-if="isDark" :size="19" /><Moon v-else :size="19" />
        </button></UiTooltip>
        <div ref="accountMenuRoot" class="account-menu-root" :class="{ open: menuOpen }" @focusout="closeAccountMenuOnFocusOut">
          <button ref="accountMenuButton" class="topbar-control account-button" aria-label="打开用户菜单" title="用户菜单" aria-haspopup="menu" :aria-expanded="menuOpen" @click="toggleAccountMenu"><UserRound :size="19" /></button>
          <div v-if="menuOpen" class="account-menu" role="menu">
            <button ref="accountMenuItem" class="account-menu-item" type="button" role="menuitem" @click="logout"><LogOut :size="17" />退出登录</button>
          </div>
        </div>
      </div>
    </header>
    <main class="page">
      <RouterView v-slot="{ Component, route }">
        <Transition name="page-fade" mode="out-in">
          <div :key="route.path" class="route-page">
            <component :is="Component" />
          </div>
        </Transition>
      </RouterView>
    </main>
  </div>
  <ToastHost v-if="showToastHost" />
</template>
