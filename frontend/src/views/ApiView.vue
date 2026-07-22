<script setup lang="ts">
import '@fontsource/jetbrains-mono/latin-400.css'
import { computed, onMounted, ref } from 'vue'
import { BookOpen, Check, Copy, KeyRound, LoaderCircle, Plus, RefreshCw, ShieldCheck, Terminal, Trash2, Zap } from '@lucide/vue'
import { api, deleteJSON, postJSON } from '../api'
import { toast } from '../toast'
import type { ApiToken, TokenScope } from '../types'
import ConfirmDialog from '../components/ui/ConfirmDialog.vue'
import UiCheckbox from '../components/ui/UiCheckbox.vue'

const tokens = ref<ApiToken[]>([])
const loading = ref(true)
const loadFailed = ref(false)
const creating = ref(false)
const tokenName = ref('PicGo')
const tokenExpiresAt = ref('')
const tokenScopes = ref<TokenScope[]>(['images:upload'])
const newToken = ref('')
const revokeTarget = ref<ApiToken | null>(null)
const revokeOpen = ref(false)
const revokeBusy = ref(false)
const copied = ref('')
const origin = window.location.origin
const apiBase = `${origin}/api/v1`

const scopeOptions: { value: TokenScope; label: string; note: string }[] = [
  { value: 'images:upload', label: '上传图片', note: 'PicGo 和上传脚本只需此权限' },
  { value: 'images:read', label: '读取图库', note: '获取图片列表和详情' },
  { value: 'images:manage', label: '整理图库', note: '管理收藏、标签和相册' },
  { value: 'images:delete', label: '删除图片', note: '移入回收站或永久删除' },
  { value: 'settings:admin', label: '系统管理', note: '包含设置、存储和 Token 完整权限' },
]

const curlExample = computed(() => `curl -X POST '${apiBase}/images' \\\n+  -H 'Authorization: Bearer YOUR_TOKEN' \\\n+  -F 'file=@/path/to/image.png'`)

const jsExample = computed(() => `const form = new FormData()
form.append('file', file)

const response = await fetch('${apiBase}/images', {
  method: 'POST',
  headers: { Authorization: 'Bearer YOUR_TOKEN' },
  body: form
})
const { data } = await response.json()
console.log(data.url)`)

function toggleScope(scope: TokenScope, enabled: boolean) {
  tokenScopes.value = enabled ? [...new Set([...tokenScopes.value, scope])] : tokenScopes.value.filter(item => item !== scope)
}

async function loadTokens() {
  loading.value = true
  loadFailed.value = false
  try { tokens.value = await api<ApiToken[]>('/tokens') }
  catch (error) { loadFailed.value = true; toast(error instanceof Error ? error.message : 'Token 读取失败', 'error') }
  finally { loading.value = false }
}

async function createToken() {
  if (!tokenName.value.trim() || !tokenScopes.value.length) { toast('请填写 Token 名称并至少选择一项权限', 'error'); return }
  creating.value = true
  try {
    const result = await postJSON<{ token: string }>('/tokens', {
      name: tokenName.value.trim(), scopes: tokenScopes.value,
      expires_at: tokenExpiresAt.value ? new Date(tokenExpiresAt.value).toISOString() : undefined,
    })
    newToken.value = result.token
    tokenName.value = ''
    tokenExpiresAt.value = ''
    tokenScopes.value = ['images:upload']
    await loadTokens()
    toast('Token 已创建，它只会完整显示这一次')
  } catch (error) { toast(error instanceof Error ? error.message : '创建失败', 'error') }
  finally { creating.value = false }
}

function requestRevoke(item: ApiToken) { revokeTarget.value = item; revokeOpen.value = true }
async function confirmRevoke() {
  if (!revokeTarget.value) return
  revokeBusy.value = true
  try { await deleteJSON(`/tokens/${revokeTarget.value.id}`); tokens.value = tokens.value.filter(item => item.id !== revokeTarget.value?.id); toast('Token 已撤销') }
  catch (error) { toast(error instanceof Error ? error.message : '撤销失败', 'error') }
  finally { revokeBusy.value = false; revokeOpen.value = false; revokeTarget.value = null }
}

async function copy(value: string, key: string) {
  await navigator.clipboard.writeText(value)
  copied.value = key
  toast('已复制到剪贴板')
  window.setTimeout(() => { if (copied.value === key) copied.value = '' }, 1600)
}

function formatDate(value: string | null) {
  return value ? new Intl.DateTimeFormat('zh-CN', { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(value)) : '从未使用'
}
function scopeLabel(scope: TokenScope) { return scopeOptions.find(item => item.value === scope)?.label || scope }

onMounted(() => void loadTokens())
</script>

<template>
  <section class="api-page content-stack">
    <header class="page-heading api-heading">
      <div><span class="eyebrow"><Zap :size="14"/> 开发者中心</span><h1>API 接入</h1><p>创建独立密钥，将轻羽图床接入 PicGo、脚本或你的应用。</p></div>
      <button class="soft-button" @click="copy(apiBase, 'base')"><Check v-if="copied === 'base'" :size="17"/><Copy v-else :size="17"/>复制 API 地址</button>
    </header>

    <div class="api-overview">
      <article><span><Terminal :size="20"/></span><div><small>API 根地址</small><code>{{ apiBase }}</code></div></article>
      <article><span><ShieldCheck :size="20"/></span><div><small>认证方式</small><strong>Bearer Token</strong></div></article>
      <article><span><Zap :size="20"/></span><div><small>上传端点</small><code>POST /images</code></div></article>
    </div>

    <section class="api-card token-manager">
      <div class="section-title"><div><span class="title-icon"><KeyRound :size="20"/></span><div><h2>API Token 管理</h2><p>按用途创建 Token，建议仅授予所需的最小权限。</p></div></div><span class="count-badge">{{ tokens.length }} 个</span></div>
      <div class="token-form">
        <label>Token 名称<input v-model="tokenName" maxlength="100" placeholder="例如：PicGo - MacBook"></label>
        <label>过期时间 <small>可选</small><input v-model="tokenExpiresAt" type="datetime-local"></label>
        <button class="primary-button" :disabled="creating" @click="createToken"><LoaderCircle v-if="creating" class="spin" :size="17"/><Plus v-else :size="17"/>{{ creating ? '创建中…' : '创建 Token' }}</button>
      </div>
      <div class="scope-grid">
        <label v-for="scope in scopeOptions" :key="scope.value" :class="{ selected: tokenScopes.includes(scope.value) }">
          <UiCheckbox :model-value="tokenScopes.includes(scope.value)" :aria-label="scope.label" @update:model-value="toggleScope(scope.value, $event)" />
          <span><strong>{{ scope.label }}</strong><small>{{ scope.note }}</small></span>
        </label>
      </div>
      <div v-if="newToken" class="token-reveal"><div><KeyRound :size="18"/><strong>请立即保存新 Token</strong><span>关闭或刷新页面后将无法再次查看。</span></div><code>{{ newToken }}</code><button class="soft-button" @click="copy(newToken, 'token')"><Check v-if="copied === 'token'" :size="17"/><Copy v-else :size="17"/>{{ copied === 'token' ? '已复制' : '复制 Token' }}</button></div>
      <div v-if="loading" class="token-state"><LoaderCircle class="spin" :size="24"/>正在读取 Token…</div>
      <div v-else-if="loadFailed" class="token-state"><span>Token 列表读取失败</span><button class="soft-button" @click="loadTokens"><RefreshCw :size="16"/>重试</button></div>
      <div v-else class="api-token-list">
        <article v-for="item in tokens" :key="item.id"><span class="token-key"><KeyRound :size="18"/></span><div><strong>{{ item.name }}</strong><small>{{ item.scopes.map(scopeLabel).join('、') }}</small></div><dl><div><dt>最近使用</dt><dd>{{ formatDate(item.last_used_at) }}</dd></div><div><dt>有效期</dt><dd>{{ item.expires_at ? formatDate(item.expires_at) : '永不过期' }}</dd></div></dl><button class="icon-danger" :aria-label="`撤销 ${item.name}`" @click="requestRevoke(item)"><Trash2 :size="17"/></button></article>
        <div v-if="!tokens.length" class="token-state">还没有 Token，创建一个即可开始接入。</div>
      </div>
    </section>

    <section class="api-card guide-card">
      <div class="section-title"><div><span class="title-icon"><Zap :size="20"/></span><div><h2>PicGo 快速接入</h2><p>使用 Web Uploader 插件，几分钟内完成配置。</p></div></div><span class="recommend-badge">推荐</span></div>
      <ol class="steps">
        <li><span>1</span><div><h3>安装插件</h3><p>在 PicGo 的“插件设置”中搜索并安装 <code>web-uploader</code>。</p><div class="inline-code"><code>picgo install web-uploader</code><button @click="copy('picgo install web-uploader', 'picgo-install')"><Check v-if="copied === 'picgo-install'" :size="15"/><Copy v-else :size="15"/></button></div></div></li>
        <li><span>2</span><div><h3>创建上传 Token</h3><p>在上方创建一个仅包含“上传图片”权限的 Token，并复制保存。</p></div></li>
        <li><span>3</span><div><h3>填写 Web Uploader 配置</h3><div class="config-table"><div><span>API 地址</span><code>{{ apiBase }}/images</code></div><div><span>请求方式</span><code>POST</code></div><div><span>文件字段名</span><code>file</code></div><div><span>请求头</span><code>Authorization: Bearer YOUR_TOKEN</code></div><div><span>JSON 返回路径</span><code>data.url</code></div></div></div></li>
        <li><span>4</span><div><h3>设为默认图床并测试</h3><p>选择 Web Uploader 为默认图床，拖入一张图片，上传成功后链接会自动复制。</p></div></li>
      </ol>
    </section>

    <section class="api-card code-guide">
      <div class="section-title"><div><span class="title-icon"><BookOpen :size="20"/></span><div><h2>通用 API 教程</h2><p>所有响应均使用 <code>{ success, data, request_id }</code> 结构。</p></div></div></div>
      <div class="code-columns"><article><header><strong>cURL</strong><button @click="copy(curlExample, 'curl')"><Check v-if="copied === 'curl'" :size="15"/><Copy v-else :size="15"/>复制</button></header><pre><code>{{ curlExample }}</code></pre></article><article><header><strong>JavaScript</strong><button @click="copy(jsExample, 'js')"><Check v-if="copied === 'js'" :size="15"/><Copy v-else :size="15"/>复制</button></header><pre><code>{{ jsExample }}</code></pre></article></div>
      <div class="endpoint-list"><article><span class="method post">POST</span><code>/images</code><p>上传本地图片</p><small>images:upload</small></article><article><span class="method post">POST</span><code>/images/import-url</code><p>从 URL 导入图片</p><small>images:upload</small></article><article><span class="method get">GET</span><code>/images</code><p>分页读取图库</p><small>images:read</small></article><article><span class="method delete">DELETE</span><code>/images/:id</code><p>将图片移入回收站</p><small>images:delete</small></article></div>
    </section>
  </section>
  <ConfirmDialog v-model:open="revokeOpen" title="撤销这个 Token？" :description="`“${revokeTarget?.name || ''}”将立即失效，使用它的客户端将无法继续调用 API。`" confirm-label="撤销 Token" :busy="revokeBusy" @confirm="confirmRevoke" />
</template>

<style scoped>
.api-page{gap:24px}.api-heading{align-items:center}.eyebrow{margin-bottom:8px;display:flex;align-items:center;gap:5px;color:var(--primary);font-size:11px;font-weight:800;letter-spacing:.1em;text-transform:uppercase}.api-overview{display:grid;grid-template-columns:1.4fr 1fr 1fr;gap:12px}.api-overview article{min-width:0;padding:17px 18px;display:flex;align-items:center;gap:13px;border:1px solid var(--line);border-radius:var(--radius-md);background:var(--surface);box-shadow:var(--shadow-xs)}.api-overview article>span,.title-icon{width:38px;height:38px;display:grid;place-items:center;flex:none;border-radius:var(--radius);color:var(--primary);background:var(--primary-soft)}.api-overview article>div{min-width:0;display:grid;gap:4px}.api-overview small{color:var(--muted)}.api-overview code{overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.api-card{padding:25px;border:1px solid var(--line);border-radius:var(--radius-lg);background:var(--surface);box-shadow:var(--shadow-xs)}.section-title,.section-title>div{display:flex;align-items:center;gap:12px}.section-title{justify-content:space-between;margin-bottom:22px}.section-title h2{font-size:18px}.section-title p{margin-top:3px;color:var(--muted);font-size:12px}.count-badge,.recommend-badge{padding:5px 9px;border-radius:999px;background:var(--raised);color:var(--muted);font-size:11px;font-weight:700}.recommend-badge{color:var(--primary);background:var(--primary-soft)}.token-form{display:grid;grid-template-columns:1fr 1fr auto;align-items:end;gap:12px}.token-form label{display:grid;gap:7px;color:var(--neutral);font-size:12px;font-weight:700}.token-form label small{color:var(--muted);font-weight:400}.token-form input{width:100%;height:42px;padding:0 12px;border:1px solid transparent;border-radius:var(--radius);outline:0;background:var(--raised)}.token-form input:focus{border-color:var(--text)}.scope-grid{margin-top:15px;display:grid;grid-template-columns:repeat(5,1fr);gap:8px}.scope-grid>label{min-width:0;padding:12px;display:flex;align-items:flex-start;gap:9px;border:1px solid var(--line);border-radius:var(--radius);cursor:pointer}.scope-grid>label.selected{border-color:color-mix(in srgb,var(--primary) 55%,var(--line));background:var(--primary-soft)}.scope-grid span{min-width:0;display:grid;gap:3px}.scope-grid strong{font-size:12px}.scope-grid small{color:var(--muted);font-size:10px;line-height:1.45}.token-reveal{margin-top:16px;padding:16px;display:grid;grid-template-columns:1fr auto;gap:11px 14px;border:1px solid color-mix(in srgb,var(--primary) 45%,var(--line));border-radius:var(--radius);background:var(--primary-soft)}.token-reveal>div{display:flex;align-items:center;gap:7px;color:var(--primary)}.token-reveal>div span{color:var(--muted);font-size:11px}.token-reveal>code{align-self:center;overflow-wrap:anywhere;color:var(--text)}.token-reveal>.soft-button{grid-row:1/3;grid-column:2}.api-token-list{margin-top:18px;border-top:1px solid var(--line)}.api-token-list>article{min-height:72px;padding:12px 4px;display:grid;grid-template-columns:auto minmax(150px,1fr) auto auto;align-items:center;gap:12px;border-bottom:1px solid var(--line)}.token-key{width:36px;height:36px;display:grid;place-items:center;border-radius:var(--radius);background:var(--raised);color:var(--primary)}.api-token-list article>div{display:grid;gap:3px}.api-token-list small{color:var(--muted);font-size:10px}.api-token-list dl{margin:0;display:flex;gap:28px}.api-token-list dl>div{min-width:120px;display:grid;gap:2px}.api-token-list dt{color:var(--muted);font-size:10px}.api-token-list dd{margin:0;font-size:11px}.icon-danger{width:36px;height:36px;display:grid;place-items:center;border:0;border-radius:var(--radius);background:transparent;color:var(--muted)}.icon-danger:hover{color:var(--danger);background:color-mix(in srgb,var(--danger) 10%,transparent)}.token-state{min-height:80px;display:flex;align-items:center;justify-content:center;gap:10px;color:var(--muted)}.steps{margin:0;padding:0;display:grid;list-style:none}.steps>li{position:relative;display:grid;grid-template-columns:38px 1fr;gap:14px;padding-bottom:24px}.steps>li:last-child{padding-bottom:0}.steps>li:not(:last-child)::before{content:"";position:absolute;top:38px;bottom:0;left:18px;width:1px;background:var(--line)}.steps>li>span{width:38px;height:38px;display:grid;place-items:center;z-index:1;border-radius:50%;background:var(--primary);color:var(--on-primary);font-weight:800}.steps h3{padding-top:2px;font-size:14px}.steps p{margin-top:5px;color:var(--muted);font-size:12px}.inline-code{width:min(430px,100%);margin-top:10px;padding:9px 10px 9px 13px;display:flex;justify-content:space-between;border-radius:var(--radius);background:var(--preview-bg)}.inline-code button,.code-columns header button{display:flex;align-items:center;gap:5px;border:0;background:transparent;color:var(--muted);font-size:11px}.config-table{margin-top:12px;overflow:hidden;border:1px solid var(--line);border-radius:var(--radius)}.config-table>div{padding:9px 12px;display:grid;grid-template-columns:150px 1fr;gap:12px;border-bottom:1px solid var(--line);font-size:12px}.config-table>div:last-child{border:0}.config-table span{color:var(--muted)}.config-table code{overflow-wrap:anywhere}.code-columns{display:grid;grid-template-columns:1fr 1fr;gap:12px}.code-columns article{min-width:0;overflow:hidden;border:1px solid var(--line);border-radius:var(--radius);background:var(--preview-bg)}.code-columns header{height:42px;padding:0 13px;display:flex;align-items:center;justify-content:space-between;border-bottom:1px solid var(--line);font-size:12px}.code-columns pre{min-height:190px;margin:0;padding:16px;overflow:auto;color:var(--neutral);font-size:11px;line-height:1.7}.endpoint-list{margin-top:16px;display:grid}.endpoint-list article{padding:11px 4px;display:grid;grid-template-columns:62px 180px 1fr auto;align-items:center;gap:12px;border-bottom:1px solid var(--line);font-size:12px}.endpoint-list p,.endpoint-list small{color:var(--muted)}.method{width:max-content;padding:3px 7px;border-radius:6px;font-size:9px;font-weight:900}.method.post{color:#e9a23b;background:rgba(233,162,59,.12)}.method.get{color:#5d9cec;background:rgba(93,156,236,.12)}.method.delete{color:var(--danger);background:color-mix(in srgb,var(--danger) 10%,transparent)}
@media(max-width:1000px){.scope-grid{grid-template-columns:repeat(2,1fr)}.api-token-list dl{display:none}}
@media(max-width:700px){.api-overview,.code-columns{grid-template-columns:1fr}.token-form{grid-template-columns:1fr}.scope-grid{grid-template-columns:1fr}.api-card{padding:18px 15px}.section-title{align-items:flex-start}.section-title p{max-width:240px}.token-reveal{grid-template-columns:1fr}.token-reveal>div{align-items:flex-start;flex-wrap:wrap}.token-reveal>code{font-size:11px}.token-reveal>.soft-button{grid-row:auto;grid-column:auto;width:100%}.api-token-list>article{grid-template-columns:auto 1fr auto}.config-table>div{grid-template-columns:1fr;gap:4px}.endpoint-list article{grid-template-columns:58px 1fr auto}.endpoint-list p{grid-column:2/4}.endpoint-list small{grid-row:1;grid-column:3}.code-columns pre{min-height:0}.api-heading>.soft-button{width:100%}}
</style>
