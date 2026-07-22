<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { SlidersHorizontal, Database, ShieldCheck, Info, Save, Plus, Server, Cloud, Globe2, Send, ChevronDown, Wifi, CheckCircle2, Trash2, KeyRound, Copy, LogOut, LoaderCircle, Eye, EyeOff, RefreshCw } from '@lucide/vue'
import { CollapsibleContent, CollapsibleRoot, CollapsibleTrigger, TabsContent, TabsList, TabsRoot, TabsTrigger } from 'reka-ui'
import { api, deleteJSON, postJSON, putJSON } from '../api'
import { toast } from '../toast'
import { useAuthStore } from '../stores/auth'
import type { Album, ApiToken, Settings, StorageRecord, Tag, TokenScope } from '../types'
import {
  linkFormatOptions,
  linkSeparatorOptions,
  readCopyPreferences,
  writeCopyPreferences,
  type LinkFormat,
  type LinkSeparator,
} from '../linkFormats'
import { defaultProcessingSettings, normalizeProcessingSettings } from '../processingSettings'
import ConfirmDialog from '../components/ui/ConfirmDialog.vue'
import UiCheckbox from '../components/ui/UiCheckbox.vue'
import UiSelect from '../components/ui/UiSelect.vue'
import UiSwitch from '../components/ui/UiSwitch.vue'
import UiTooltip from '../components/ui/UiTooltip.vue'

const auth = useAuthStore()
const tab = ref<'base' | 'storage' | 'security' | 'system'>('base')
const loading = ref(true)
const loadFailed = ref(false)
const saving = ref(false)
const testing = ref('')
const expanded = ref('')
const settings = ref<Settings>({ site_name: '轻羽图床', site_url: '', default_storage_id: 'local', max_file_size: 20 << 20, max_batch_count: 10, allowed_types: ['image/jpeg', 'image/png', 'image/gif', 'image/webp'], naming_rule: 'random', allow_duplicates: false, random: { enabled: false, album_id: '', tag_id: '' }, processing: { ...defaultProcessingSettings } })
const storages = ref<StorageRecord[]>([])
const albums = ref<Album[]>([])
const tags = ref<Tag[]>([])
const tokens = ref<ApiToken[]>([])
const system = ref({ version: '-', database: '-', enabled_storages: 0 })
const tokenName = ref('')
const tokenExpiresAt = ref('')
const tokenScopes = ref<TokenScope[]>(['images:upload'])
const newToken = ref('')
const password = reactive({ current_password: '', new_password: '', confirm: '' })
const passwordVisible = ref(false)
const drafts = reactive<Record<string, StorageRecord>>({})
const dangerOpen = ref(false)
const dangerBusy = ref(false)
const dangerTarget = ref<{ kind: 'storage'; item: StorageRecord } | { kind: 'token'; item: ApiToken } | null>(null)
const initialCopyPreferences = readCopyPreferences()
const copyFormat = ref<LinkFormat>(initialCopyPreferences.format)
const autoCopy = ref(initialCopyPreferences.autoCopy)
const copySeparator = ref<LinkSeparator>(initialCopyPreferences.separator)

const tabs = [
  { id: 'base', label: '基础设置', icon: SlidersHorizontal }, { id: 'storage', label: '存储管理', icon: Database },
  { id: 'security', label: '安全与令牌', icon: ShieldCheck }, { id: 'system', label: '系统信息', icon: Info },
] as const
const typeInfo = { local: { label: '本地存储', icon: Server, note: '使用服务器本地磁盘存储文件' }, s3: { label: 'S3 兼容存储', icon: Cloud, note: '适用于 AWS S3、R2、OSS 等对象存储' }, webdav: { label: 'WebDAV', icon: Globe2, note: '通过 WebDAV 协议访问远程服务器' }, telegram: { label: 'Telegram', icon: Send, note: '将图片作为文件发送到频道或群组' } }
const storageTypeOptions = [
  { label: '本地存储', value: 'local' },
  { label: 'S3 兼容存储', value: 's3' },
  { label: 'WebDAV', value: 'webdav' },
  { label: 'Telegram', value: 'telegram' },
]
const typeFields: Record<string, { key: string; label: string; secret?: boolean; switch?: boolean; placeholder?: string }[]> = {
  local: [{ key: 'data_dir', label: '数据目录', placeholder: 'images' }, { key: 'public_url', label: '外部访问地址', placeholder: 'https://img.example.com/files' }],
  s3: [{ key: 'endpoint', label: 'Endpoint', placeholder: 'https://s3.example.com' }, { key: 'region', label: 'Region', placeholder: 'us-east-1' }, { key: 'bucket', label: 'Bucket' }, { key: 'access_key', label: 'Access Key', secret: true }, { key: 'secret_key', label: 'Secret Key', secret: true }, { key: 'public_url', label: '外部访问地址（R2 填图床地址）', placeholder: '例如 https://img.example.com；留空使用相对地址' }, { key: 'path_style', label: '路径风格（Path Style）', switch: true }],
  webdav: [{ key: 'url', label: '服务地址' }, { key: 'username', label: '用户名' }, { key: 'password', label: '密码', secret: true }, { key: 'target_dir', label: '目标目录' }, { key: 'public_url', label: '访问域名' }],
  telegram: [{ key: 'bot_token', label: 'Bot Token', secret: true }, { key: 'chat_id', label: '频道或群组 ID' }, { key: 'proxy_url', label: 'Telegram API 代理地址（可选）', placeholder: 'https://tg-api.example.com' }],
}
const enabledStorages = computed(() => storages.value.filter((item) => item.enabled))
const enabledStorageOptions = computed(() => enabledStorages.value.map((item) => ({ label: item.name, value: item.id })))
const randomAlbumOptions = computed(() => [{ label: '全部相册', value: '' }, ...albums.value.map((item) => ({ label: item.name, value: item.id }))])
const randomTagOptions = computed(() => [{ label: '全部标签', value: '' }, ...tags.value.map((item) => ({ label: item.name, value: item.id }))])
const namingRuleOptions = [
  { label: '随机 ID', value: 'random' },
  { label: '日期路径', value: 'date' },
  { label: '保留原文件名', value: 'original' },
]
const allowedFormats = [{ value: 'image/jpeg', label: 'JPEG' }, { value: 'image/png', label: 'PNG' }, { value: 'image/gif', label: 'GIF' }, { value: 'image/webp', label: 'WebP' }]
const watermarkPositionOptions = [
  { label: '左上角', value: 'top-left' },
  { label: '右上角', value: 'top-right' },
  { label: '左下角', value: 'bottom-left' },
  { label: '右下角', value: 'bottom-right' },
  { label: '居中', value: 'center' },
]
const tokenScopeOptions: { value: TokenScope; label: string; note: string }[] = [
  { value: 'images:upload', label: '上传图片', note: '适合 PicGo 和上传脚本' },
  { value: 'images:read', label: '读取图库', note: '读取图片、存储名称和系统信息' },
  { value: 'images:manage', label: '整理图库', note: '收藏、标签、相册和恢复' },
  { value: 'images:delete', label: '删除图片', note: '移入回收站和永久删除' },
  { value: 'settings:admin', label: '系统管理', note: '完整管理权限，包括存储、设置和 Token' },
]
const dangerTitle = computed(() => dangerTarget.value?.kind === 'storage' ? '删除这个存储？' : '撤销这个 Token？')
const dangerDescription = computed(() => {
  if (!dangerTarget.value) return ''
  return dangerTarget.value.kind === 'storage'
    ? `“${dangerTarget.value.item.name}”的存储配置将被删除。`
    : `Token“${dangerTarget.value.item.name}”将立即失效，使用它的客户端无法再上传。`
})

function clone<T>(value: T): T { return JSON.parse(JSON.stringify(value)) }
function setStorageOpen(item: StorageRecord, open: boolean) { if (open) drafts[item.id] = clone(item); expanded.value = open ? item.id : '' }
function toggleAllowedType(value: string, enabled: boolean) { settings.value.allowed_types = enabled ? [...new Set([...settings.value.allowed_types, value])] : settings.value.allowed_types.filter((item) => item !== value) }
function toggleTokenScope(value: TokenScope, enabled: boolean) {
  tokenScopes.value = enabled ? [...new Set([...tokenScopes.value, value])] : tokenScopes.value.filter((item) => item !== value)
}
function addStorage() {
  const id = `storage-${Date.now().toString(36)}`
  const item: StorageRecord = { id, name: '新的本地存储', type: 'local', enabled: true, config: {} }
  storages.value.push(item); drafts[id] = clone(item); expanded.value = id
}
function updateNewStorageType(item: StorageRecord, value: string) {
  if (item.created_at || !drafts[item.id] || !(value in typeInfo)) return
  const type = value as StorageRecord['type']
  item.type = type
  item.name = `新的${typeInfo[type].label}`
  drafts[item.id].type = type
  drafts[item.id].name = item.name
  drafts[item.id].config = {}
}
async function saveSettings() { saving.value = true; try { settings.value = await putJSON<Settings>('/settings', settings.value); toast('基础设置已保存') } catch (e) { toast(e instanceof Error ? e.message : '保存失败', 'error') } finally { saving.value = false } }
async function saveStorage(id: string) {
  saving.value = true
  try { const saved = await putJSON<StorageRecord>(`/storages/${id}`, drafts[id]); const index = storages.value.findIndex((item) => item.id === id); storages.value[index] = saved; drafts[id] = clone(saved); toast('存储配置已保存') }
  catch (e) { toast(e instanceof Error ? e.message : '保存失败', 'error') }
  finally { saving.value = false }
}
async function testStorage(id: string) { testing.value = id; try { await postJSON(`/storages/test?storage_id=${encodeURIComponent(id)}`, drafts[id]); toast('连接成功，配置尚未保存') } catch (e) { toast(e instanceof Error ? e.message : '连接失败', 'error') } finally { testing.value = '' } }
async function deleteStorage(item: StorageRecord) { try { await deleteJSON(`/storages/${item.id}`); storages.value = storages.value.filter((value) => value.id !== item.id); toast('存储已删除') } catch (e) { toast(e instanceof Error ? e.message : '删除失败', 'error') } }
async function createToken() {
  if (!tokenName.value.trim() || !tokenScopes.value.length) {
    toast('请填写 Token 名称并至少选择一项权限', 'error')
    return
  }
  try {
    const result = await postJSON<{ token: string }>('/tokens', {
      name: tokenName.value,
      scopes: tokenScopes.value,
      expires_at: tokenExpiresAt.value ? new Date(tokenExpiresAt.value).toISOString() : undefined,
    })
    newToken.value = result.token
    tokenName.value = ''
    tokenExpiresAt.value = ''
    tokenScopes.value = ['images:upload']
    tokens.value = await api<ApiToken[]>('/tokens')
    toast('Token 已创建，请立即复制保存')
  } catch (e) { toast(e instanceof Error ? e.message : '创建失败', 'error') }
}
async function revokeToken(item: ApiToken) { try { await deleteJSON(`/tokens/${item.id}`); tokens.value = tokens.value.filter((token) => token.id !== item.id); toast('Token 已撤销') } catch (e) { toast(e instanceof Error ? e.message : '撤销失败', 'error') } }
function requestDanger(target: NonNullable<typeof dangerTarget.value>) { dangerTarget.value = target; dangerOpen.value = true }
async function confirmDanger() { const target = dangerTarget.value; if (!target) return; dangerBusy.value = true; try { if (target.kind === 'storage') await deleteStorage(target.item); else await revokeToken(target.item) } finally { dangerBusy.value = false; dangerOpen.value = false; dangerTarget.value = null } }
async function copyToken() { await navigator.clipboard.writeText(newToken.value); toast('Token 已复制') }
async function changePassword() { if (password.new_password !== password.confirm) { toast('两次输入的新密码不一致', 'error'); return } try { await putJSON('/auth/password', { current_password: password.current_password, new_password: password.new_password }); toast('密码已修改，请重新登录'); auth.reset() } catch (e) { toast(e instanceof Error ? e.message : '修改失败', 'error') } }
function formatDate(value: string | null) { return value ? new Intl.DateTimeFormat('zh-CN', { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(value)) : '从未使用' }
function tokenScopeLabel(scope: TokenScope) { return tokenScopeOptions.find((item) => item.value === scope)?.label || scope }

watch([copyFormat, autoCopy, copySeparator], () => {
  writeCopyPreferences({
    format: copyFormat.value,
    autoCopy: autoCopy.value,
    separator: copySeparator.value,
  })
})

async function loadSettingsData() {
  loading.value = true
  loadFailed.value = false
  try {
    const result = await Promise.all([api<Settings>('/settings'), api<StorageRecord[]>('/storages'), api<ApiToken[]>('/tokens'), api<typeof system.value>('/system'), api<Album[]>('/albums'), api<Tag[]>('/tags')])
    settings.value = { ...result[0], random: { enabled: Boolean(result[0].random?.enabled), album_id: result[0].random?.album_id || '', tag_id: result[0].random?.tag_id || '' }, processing: normalizeProcessingSettings(result[0].processing) }
    storages.value = result[1]
    tokens.value = result[2]
    system.value = result[3]
    albums.value = result[4]
    tags.value = result[5]
    storages.value.forEach((item) => drafts[item.id] = clone(item))
  } catch (error) {
    loadFailed.value = true
    toast(error instanceof Error ? error.message : '设置读取失败', 'error')
  } finally { loading.value = false }
}
onMounted(() => void loadSettingsData())
</script>

<template>
  <TabsRoot v-model="tab" class="settings-layout" orientation="vertical">
    <TabsList class="settings-nav" aria-label="系统设置分类"><h2>系统设置</h2><TabsTrigger v-for="item in tabs" :key="item.id" :value="item.id" :class="{ active: tab === item.id }"><component :is="item.icon" :size="20"/>{{ item.label }}</TabsTrigger><div class="feather-decor">⌁</div></TabsList>
    <div v-if="loading" class="settings-main gallery-state"><LoaderCircle class="spin" :size="28"/>正在读取设置…</div>
    <div v-else-if="loadFailed" class="settings-main gallery-state"><Info :size="36"/><h2>系统设置暂时无法读取</h2><button class="soft-button" @click="loadSettingsData"><RefreshCw :size="17"/>重新加载</button></div>
    <template v-else>
      <TabsContent value="base" class="settings-main">
        <header class="settings-heading"><div><h1>基础设置</h1></div><button class="primary-button" :disabled="saving" @click="saveSettings"><Save :size="18"/>保存设置</button></header>
        <form class="form-section" @submit.prevent="saveSettings"><h2>站点信息</h2><div class="form-grid"><label>站点名称<input v-model="settings.site_name" maxlength="100"></label><label>站点访问地址<input v-model="settings.site_url" type="url" placeholder="https://img.example.com"></label><label>默认存储<UiSelect v-model="settings.default_storage_id" :options="enabledStorageOptions" aria-label="默认存储" /></label></div></form>
        <section class="form-section"><h2>上传规则</h2><div class="form-grid"><label>单文件上限（MB）<input :value="settings.max_file_size / 1024 / 1024" type="number" min="1" max="1024" @input="settings.max_file_size = Number(($event.target as HTMLInputElement).value) * 1024 * 1024"></label><label>单批文件数量<input v-model.number="settings.max_batch_count" type="number" min="1" max="100"></label><label>图片命名规则<UiSelect v-model="settings.naming_rule" :options="namingRuleOptions" aria-label="图片命名规则" /></label></div><div class="checkbox-row"><span>允许格式</span><label v-for="item in allowedFormats" :key="item.value"><UiCheckbox :model-value="settings.allowed_types.includes(item.value)" :aria-label="item.label" @update:model-value="toggleAllowedType(item.value, $event)" />{{ item.label }}</label></div><label class="switch-row"><span><strong>允许重复文件</strong></span><UiSwitch v-model="settings.allow_duplicates" aria-label="允许重复文件" /></label></section>
        <section class="form-section processing-settings"><h2>图片处理</h2><label class="switch-row"><span><strong>清理 EXIF 与定位元数据</strong></span><UiSwitch v-model="settings.processing.strip_metadata" aria-label="清理图片元数据" /></label><label class="switch-row"><span><strong>生成 WebP 版本</strong></span><UiSwitch v-model="settings.processing.generate_webp" aria-label="生成 WebP 版本" /></label><div class="form-grid processing-fields"><label>WebP 质量<input v-model.number="settings.processing.webp_quality" type="number" min="1" max="100" :disabled="!settings.processing.generate_webp"></label></div><label class="switch-row"><span><strong>生成水印版本</strong></span><UiSwitch v-model="settings.processing.watermark_enabled" aria-label="生成水印版本" /></label><div class="form-grid processing-fields"><label>水印文字<input v-model.trim="settings.processing.watermark_text" maxlength="200" :disabled="!settings.processing.watermark_enabled" placeholder="例如 Feather ImgBed"></label><label>水印位置<UiSelect v-model="settings.processing.watermark_position" :options="watermarkPositionOptions" aria-label="水印位置" /></label></div></section>
        <section class="form-section"><h2>随机图隐私</h2><label class="switch-row"><span><strong>启用公开随机图 API</strong><small>关闭时 /api/v1/random 返回 404，避免私人图片意外公开。</small></span><UiSwitch v-model="settings.random.enabled" aria-label="启用公开随机图 API" /></label><div class="form-grid"><label>限定相册<UiSelect v-model="settings.random.album_id" :options="randomAlbumOptions" aria-label="随机图限定相册" /></label><label>限定标签<UiSelect v-model="settings.random.tag_id" :options="randomTagOptions" aria-label="随机图限定标签" /></label></div><p class="section-note">同时选择相册和标签时，只有同时满足两个条件的图片会进入随机池。</p></section>
        <section class="form-section"><h2>复制偏好</h2><div class="form-grid"><label>默认链接格式<UiSelect v-model="copyFormat" :options="linkFormatOptions" aria-label="默认链接格式" /></label><label>批量链接分隔方式<UiSelect v-model="copySeparator" :options="linkSeparatorOptions" aria-label="批量链接分隔方式" /></label></div><label class="switch-row"><span><strong>上传完成后自动复制</strong></span><UiSwitch v-model="autoCopy" aria-label="上传完成后自动复制" /></label></section>
      </TabsContent>

      <TabsContent value="storage" class="settings-main">
        <header class="settings-heading"><div><h1>存储管理</h1></div><button class="soft-button" @click="addStorage"><Plus :size="18"/>添加存储</button></header>
        <div class="storage-list"><CollapsibleRoot v-for="item in storages" :key="item.id" as="article" class="storage-row" :class="{ expanded: expanded === item.id }" :open="expanded === item.id" @update:open="setStorageOpen(item, $event)">
          <CollapsibleTrigger as-child><button class="storage-summary"><span class="storage-icon"><component :is="typeInfo[item.type].icon" :size="23"/></span><span class="storage-title"><strong>{{ item.name }}</strong></span><span v-if="settings.default_storage_id === item.id" class="default-label">默认</span><span class="status-label" :class="{ on: item.enabled }">{{ item.enabled ? '已启用' : '未启用' }}</span><ChevronDown :size="19"/></button></CollapsibleTrigger>
          <CollapsibleContent v-if="drafts[item.id]" class="storage-form"><div class="form-grid"><label v-if="!item.created_at">存储类型<UiSelect :model-value="drafts[item.id].type" :options="storageTypeOptions" aria-label="存储类型" @update:model-value="updateNewStorageType(item, $event)" /></label><label>存储名称<input v-model="drafts[item.id].name"></label><label class="switch-row compact"><span><strong>启用此存储</strong></span><UiSwitch v-model="drafts[item.id].enabled" :aria-label="`启用${item.name}`" /></label><template v-for="field in typeFields[drafts[item.id].type]" :key="field.key"><label v-if="!field.switch">{{ field.label }}<span class="password-field"><input v-model="drafts[item.id].config[field.key] as string" :type="field.secret && !passwordVisible ? 'password' : 'text'" :placeholder="field.secret && item.config[`${field.key}_configured`] ? '留空表示保持原值' : field.placeholder"><UiTooltip v-if="field.secret" :text="passwordVisible ? '隐藏密钥' : '显示密钥'" side="left"><button type="button" :aria-label="passwordVisible ? '隐藏密钥' : '显示密钥'" @click="passwordVisible = !passwordVisible"><EyeOff v-if="passwordVisible" :size="17"/><Eye v-else :size="17"/></button></UiTooltip></span></label><label v-else class="switch-row compact"><span><strong>{{ field.label }}</strong></span><UiSwitch :model-value="Boolean(drafts[item.id].config[field.key])" :aria-label="field.label" @update:model-value="drafts[item.id].config[field.key] = $event" /></label></template></div><div class="storage-actions"><button v-if="item.id !== settings.default_storage_id" class="text-button danger" @click="requestDanger({ kind: 'storage', item })"><Trash2 :size="17"/>删除</button><span></span><button class="soft-button" :disabled="testing === item.id" @click="testStorage(item.id)"><Wifi :size="17"/>{{ testing === item.id ? '测试中…' : '测试连接' }}</button><button class="primary-button" :disabled="saving" @click="saveStorage(item.id)"><Save :size="17"/>保存配置</button></div></CollapsibleContent>
        </CollapsibleRoot></div>
      </TabsContent>

      <TabsContent value="security" class="settings-main">
        <header class="settings-heading"><div><h1>安全与令牌</h1></div></header>
        <section class="form-section"><h2>修改密码</h2><form class="form-grid" @submit.prevent="changePassword"><label>当前密码<input v-model="password.current_password" type="password" required></label><label>新密码<input v-model="password.new_password" type="password" minlength="10" required></label><label>确认新密码<input v-model="password.confirm" type="password" minlength="10" required></label><button class="soft-button form-button"><Save :size="17"/>更新密码</button></form></section>
        <section class="form-section"><h2>API Token</h2>
          <div class="token-create"><input v-model="tokenName" placeholder="Token 用途名称，例如 PicGo"><input v-model="tokenExpiresAt" type="datetime-local" aria-label="Token 过期时间"><button class="primary-button" @click="createToken"><Plus :size="17"/>创建 Token</button></div>
          <div class="checkbox-row token-scope-list"><span>Token 权限</span><label v-for="scope in tokenScopeOptions" :key="scope.value"><UiCheckbox :model-value="tokenScopes.includes(scope.value)" :aria-label="scope.label" @update:model-value="toggleTokenScope(scope.value, $event)" />{{ scope.label }}</label></div>
          <div v-if="newToken" class="new-token"><KeyRound :size="19"/><code>{{ newToken }}</code><button @click="copyToken"><Copy :size="17"/>复制</button></div>
          <div class="token-list"><article v-for="item in tokens" :key="item.id"><span class="storage-icon"><KeyRound :size="19"/></span><div><strong>{{ item.name }}</strong><small>{{ item.scopes.map(tokenScopeLabel).join('、') }} · {{ item.expires_at ? `到期 ${formatDate(item.expires_at)}` : '永不过期' }} · {{ item.last_used_at ? `最近使用 ${formatDate(item.last_used_at)}` : '从未使用' }}</small></div><button class="text-button danger" @click="requestDanger({ kind: 'token', item })">撤销</button></article><p v-if="!tokens.length" class="section-note">还没有 API Token</p></div>
        </section>
      </TabsContent>

      <TabsContent value="system" class="settings-main">
        <header class="settings-heading"><div><h1>系统信息</h1></div></header>
        <section class="system-panel"><div><span class="system-icon"><CheckCircle2 :size="23"/></span><p><strong>服务运行正常</strong></p></div><dl><div><dt>应用版本</dt><dd>{{ system.version }}</dd></div><div><dt>数据库状态</dt><dd>{{ system.database === 'ok' ? '正常' : system.database }}</dd></div><div><dt>已启用存储</dt><dd>{{ system.enabled_storages }} 个</dd></div><div><dt>界面资源版本</dt><dd>{{ system.version }}</dd></div></dl></section>
        <section class="form-section danger-zone"><h2>会话</h2><button class="soft-button danger" @click="auth.logout"><LogOut :size="17"/>退出登录</button></section>
      </TabsContent>
    </template>
  </TabsRoot>
  <ConfirmDialog v-model:open="dangerOpen" :title="dangerTitle" :description="dangerDescription" :confirm-label="dangerTarget?.kind === 'storage' ? '删除存储' : '撤销 Token'" :busy="dangerBusy" @confirm="confirmDanger" />
</template>
