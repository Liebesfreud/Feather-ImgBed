<script setup lang="ts">
import { computed, ref } from 'vue'
import { Feather, Eye, EyeOff, ArrowRight, ShieldCheck } from 'lucide-vue-next'
import { useAuthStore } from '../stores/auth'
import { ApiError } from '../api'
import UiTooltip from './ui/UiTooltip.vue'

const auth = useAuthStore()
const username = ref('')
const password = ref('')
const confirm = ref('')
const siteUrl = ref(window.location.origin)
const visible = ref(false)
const loading = ref(false)
const error = ref('')
const isSetup = computed(() => !auth.initialized)

async function submit() {
  error.value = ''
  if (isSetup.value && password.value !== confirm.value) { error.value = '两次输入的密码不一致'; return }
  loading.value = true
  try {
    if (isSetup.value) await auth.initialize(username.value, password.value, siteUrl.value)
    else await auth.login(username.value, password.value)
  } catch (e) { error.value = e instanceof ApiError ? e.message : '操作失败，请稍后重试' }
  finally { loading.value = false }
}
</script>

<template>
  <main class="auth-page">
    <section class="auth-story">
      <div class="auth-brand"><span><Feather :size="25" /></span>轻羽图床</div>
      <div class="auth-copy">
        <div class="auth-illustration" aria-hidden="true"><Feather :size="92" /></div>
        <h1>让图片抵达<br>它该去的地方。</h1>
        <p>上传、管理、分享。所有内容都由你掌控。</p>
      </div>
      <div class="auth-trust"><ShieldCheck :size="18" />凭据只保存在你的服务器中</div>
    </section>
    <section class="auth-panel">
      <form class="auth-form" @submit.prevent="submit">
        <div>
          <h2>{{ isSetup ? '开始使用轻羽' : '欢迎回来' }}</h2>
          <p>{{ isSetup ? '创建管理员账户，完成首次配置。' : '登录后继续管理你的图片。' }}</p>
        </div>
        <label>管理员用户名<input v-model="username" autocomplete="username" minlength="3" placeholder="至少 3 个字符" required></label>
        <label v-if="isSetup">站点访问地址<input v-model="siteUrl" type="url" placeholder="https://img.example.com" required></label>
        <label>密码
          <span class="password-field"><input v-model="password" :type="visible ? 'text' : 'password'" :autocomplete="isSetup ? 'new-password' : 'current-password'" minlength="10" placeholder="至少 10 个字符" required><UiTooltip :text="visible ? '隐藏密码' : '显示密码'" side="left"><button type="button" :aria-label="visible ? '隐藏密码' : '显示密码'" @click="visible = !visible"><EyeOff v-if="visible" :size="18"/><Eye v-else :size="18"/></button></UiTooltip></span>
        </label>
        <label v-if="isSetup">确认密码<input v-model="confirm" type="password" autocomplete="new-password" placeholder="再次输入密码" required></label>
        <p v-if="error" class="form-error" role="alert">{{ error }}</p>
        <button class="primary-button auth-submit" :disabled="loading">{{ loading ? '请稍候…' : isSetup ? '创建并进入' : '登录' }}<ArrowRight :size="18" /></button>
      </form>
    </section>
  </main>
</template>
