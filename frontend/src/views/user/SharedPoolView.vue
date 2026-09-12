<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import AppLayout from '@/components/layout/AppLayout.vue'
import CreateAccountModal from '@/components/account/CreateAccountModal.vue'
import EditAccountModal from '@/components/account/EditAccountModal.vue'
import AccountTestModal from '@/components/admin/account/AccountTestModal.vue'
import ReAuthAccountModal from '@/components/admin/account/ReAuthAccountModal.vue'
import PlatformIcon from '@/components/common/PlatformIcon.vue'
import PlatformTypeBadge from '@/components/common/PlatformTypeBadge.vue'
import DataTable from '@/components/common/DataTable.vue'
import type { Column } from '@/components/common/types'
import BaseDialog from '@/components/common/BaseDialog.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import Icon from '@/components/icons/Icon.vue'
import CapacityBadge from '@/components/account/CapacityBadge.vue'
import RecentRequestsCell from '@/components/account/RecentRequestsCell.vue'
import type { OpsRequestDetail } from '@/api/admin/ops'
import UseKeyModal from '@/components/keys/UseKeyModal.vue'
import { maskApiKey } from '@/utils/maskApiKey'
import { buildCcSwitchImportDeeplink, type CcSwitchClientType } from '@/utils/ccswitchImport'
import { getModelsByPlatform } from '@/composables/useModelWhitelist'
import { getPublicSettings } from '@/api/auth'
import type { Account, AccountPlatform, AccountType, GroupPlatform, PublicSettings } from '@/types'
import {
  getSharedPoolCards, getMySharedCards, getSharedWallet, transferSharedEarnings, setSharedListingStatus, setSharedListingListed, deleteSharedListing,
  getSharedAccount, listSharedAPIKeys, createSharedAPIKey, updateSharedAPIKey, deleteSharedAPIKey,
  type SharedCard, type SharedWallet, type SharedAPIKey, type SharedKeySelectionMode, type SharedKeyPriorityMode
} from '@/api/sharedPool'

const cards = ref<SharedCard[]>([])
const myCards = ref<SharedCard[]>([])
const wallet = ref<SharedWallet | null>(null)
const mode = ref<'pool' | 'mine' | 'keys'>('pool')
const loading = ref(true)
const error = ref('')
const walletError = ref('')
const transferring = ref(false)
const message = ref('')
const showCreateAccount = ref(false)
const testingCard = ref<any>(null)
const editingAccount = ref<Account | null>(null)
const showEditAccount = ref(false)
const reAuthAccount = ref<Account | null>(null)
const showReAuth = ref(false)
const menu = ref<{ show: boolean; card: SharedCard | null; anchorRect: DOMRect | null }>({ show: false, card: null, anchorRect: null })
const selectedCards = ref<number[]>([])
const sharedKeys = ref<SharedAPIKey[]>([])
const keyName = ref('')
const keyPlatform = ref('openai')
const keySelection = ref<SharedKeySelectionMode>('manual')
const keyPriority = ref<SharedKeyPriorityMode>('order')
const keyListings = ref<number[]>([])
const keyMessage = ref('')

const showKeyModal = ref(false)
const creatingKey = ref(false)
const editingKeyId = ref<number | null>(null)
const platformFilter = ref('')
const batchBusy = ref(false)
const availableKeyCards = computed(() => {
  const byId = new Map<number, SharedCard>()
  for (const card of [...myCards.value, ...cards.value]) {
    if (card.status === 'active' && card.platform.toLowerCase() === keyPlatform.value.toLowerCase()) byId.set(card.id, card)
  }
  return [...byId.values()]
})
const platforms = computed(() => [...new Set(cards.value.map(card => card.platform))])
const visibleCards = computed(() => platformFilter.value ? cards.value.filter(card => card.platform === platformFilter.value) : cards.value)
let transferKey: string | null = null
let generation = 0

const labels: Record<string, string> = {
  active: '运行中', paused: '已暂停', testing: '检测中', invalid: '不可用', suspended: '已停用'
}
const typeLabels: Record<string, string> = {
  oauth: 'OAuth',
  apikey: 'API Key',
  'setup-token': 'Setup Token',
  bedrock: 'Bedrock',
  service_account: 'Vertex',
  upstream: 'API Key'
}

function errorText(error: unknown) {
  if (error instanceof Error) return error.message
  if (typeof error === 'object' && error !== null && 'message' in error) return String(error.message)
  return '请求失败，请重试'
}
function typeText(card: SharedCard) {
  return typeLabels[card.type || ''] || card.type || '未知类型'
}
function capacityClass(current: number, max: number) {
  if (max > 0 && current >= max) return 'bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-400'
  if (current > 0) return 'bg-yellow-100 text-yellow-700 dark:bg-yellow-900/30 dark:text-yellow-400'
  return 'bg-gray-100 text-gray-600 dark:bg-gray-800 dark:text-gray-400'
}
function statusClass(status: string) {
  switch (status) {
    case 'active': return 'bg-emerald-50 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300'
    case 'paused': return 'bg-amber-50 text-amber-700 dark:bg-amber-900/30 dark:text-amber-300'
    case 'invalid': return 'bg-red-50 text-red-700 dark:bg-red-900/30 dark:text-red-300'
    case 'testing': return 'bg-sky-50 text-sky-700 dark:bg-sky-900/30 dark:text-sky-300'
    default: return 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-gray-300'
  }
}
function modelsFor(card: SharedCard) {
  if (card.available_models?.length) return card.available_models
  return getModelsByPlatform(card.platform)
}
function iconPlatform(value: string): any { return value }
function recentRequestsOf(card: SharedCard): OpsRequestDetail[] {
  return (card.recent_calls || []).map((call) => ({
    kind: call.result_status === 'success' ? 'success' : 'error',
    created_at: call.created_at,
    request_id: call.request_id,
    model: call.model,
    duration_ms: call.duration_ms,
    actual_cost: call.charged_amount,
    status_code: call.result_status === 'success' ? 200 : null,
    message: call.result_status === 'success' ? undefined : call.result_status
  }))
}
async function loadCards() {
  const current = ++generation
  loading.value = true
  error.value = ''
  try {
    const items = mode.value === 'mine'
      ? await getMySharedCards()
      : await getSharedPoolCards({ limit: 100, recent_limit: 8 })
    if (current === generation) {
      cards.value = items
      if (mode.value === 'mine') myCards.value = items
      if (platformFilter.value && !items.some(card => card.platform === platformFilter.value)) platformFilter.value = ''
    }
  } catch (e) {
    if (current === generation) error.value = errorText(e)
  } finally {
    if (current === generation) loading.value = false
  }
}
async function loadWallet() {
  walletError.value = ''
  try { wallet.value = await getSharedWallet() }
  catch (e) { walletError.value = errorText(e) }
}
function changeMode(next: 'pool' | 'mine' | 'keys') {
  mode.value = next
  platformFilter.value = ''
  selectedCards.value = []
  if (next === 'keys') { void loadCards(); void loadKeys(); void loadMyCards() }
  else void loadCards()
}
async function loadMyCards() {
  try { myCards.value = await getMySharedCards() }
  catch { /* keep last known */ }
}
async function transfer() {
  if (transferring.value) return
  transferring.value = true
  walletError.value = ''
  message.value = ''
  transferKey ??= crypto.randomUUID()
  try {
    const result = await transferSharedEarnings(transferKey)
    transferKey = null
    message.value = `已转入 ${result.amount}，平台余额 ${result.balance_after}`
    await loadWallet()
  } catch (e) {
    walletError.value = errorText(e)
  } finally { transferring.value = false }
}
async function toggleListed(card: SharedCard) {
  try {
    await setSharedListingListed(card.id, !(card.listed !== false))
    await loadCards()
    if (mode.value !== 'mine') await loadMyCards()
  } catch (e) { error.value = errorText(e) }
}
async function toggle(card: SharedCard) {
  // Any state other than active (including an account that was marked invalid
  // by a health check) must use the resume action.  Sending pause for an
  // invalid card leaves the listing unavailable and makes the switch appear
  // to do nothing.
  try { await setSharedListingStatus(card.id, card.status === 'active' ? 'pause' : 'resume'); await loadCards() }
  catch (e) { error.value = errorText(e) }
}
async function remove(card: SharedCard) {
  if (!window.confirm('确定删除这个共享账号吗？')) return
  try { await deleteSharedListing(card.id); await loadCards() }
  catch (e) { error.value = errorText(e) }
}
async function batchStatus(action: 'pause' | 'resume') {
  if (!selectedCards.value.length || batchBusy.value) return
  batchBusy.value = true
  error.value = ''
  try {
    await Promise.all(selectedCards.value.map(id => setSharedListingStatus(id, action)))
    selectedCards.value = []
    await loadCards()
  } catch (e) { error.value = errorText(e) }
  finally { batchBusy.value = false }
}
async function batchDelete() {
  if (!selectedCards.value.length || batchBusy.value) return
  if (!window.confirm(`确定删除选中的 ${selectedCards.value.length} 个共享账号吗？`)) return
  batchBusy.value = true
  error.value = ''
  try {
    await Promise.all(selectedCards.value.map(id => deleteSharedListing(id)))
    selectedCards.value = []
    await loadCards()
  } catch (e) { error.value = errorText(e) }
  finally { batchBusy.value = false }
}
const timeText = (value?: string) => value ? new Date(value).toLocaleString() : '暂无调用'
const avgLatency = (card: SharedCard) => {
  const xs = card.recent_calls.map(call => Number(call.duration_ms || 0)).filter(value => value > 0)
  return xs.length ? Math.round(xs.reduce((a, b) => a + b, 0) / xs.length) : 0
}
async function loadPublicSettings() {
  try { publicSettings.value = await getPublicSettings() }
  catch { /* keep defaults */ }
}
onMounted(() => { void loadCards(); void loadWallet(); void loadPublicSettings() })
async function loadKeys(){ try { sharedKeys.value = await listSharedAPIKeys() } catch {} }
function openCreate() { showCreateAccount.value = true }
async function loadOwnedAccount(card: SharedCard) {
  const id = card.account_id || card.id
  return getSharedAccount(id)
}
async function openEdit(card: SharedCard) {
  try {
    editingAccount.value = await loadOwnedAccount(card)
    showEditAccount.value = true
  } catch (e) { error.value = errorText(e) }
}
async function openReAuth(card: SharedCard) {
  try {
    reAuthAccount.value = await loadOwnedAccount(card)
    showReAuth.value = true
  } catch (e) { error.value = errorText(e) }
}
function openMenu(card: SharedCard, event: MouseEvent) {
  menu.value = { show: true, card, anchorRect: (event.currentTarget as HTMLElement).getBoundingClientRect() }
}
function closeMenu() { menu.value = { show: false, card: null, anchorRect: null } }
function canReauth(card: SharedCard) {
  return card.type === 'oauth' || card.type === 'setup-token'
}
function openTest(card: SharedCard) {
  // The admin test endpoint addresses the underlying account.  Newer card
  // responses include account_id; retain the listing-id fallback for older
  // servers so the UI remains backwards compatible.
  testingCard.value = {
    id: card.account_id || card.id,
    name: card.display_name,
    type: card.type || 'shared',
    platform: card.platform,
    status: card.status
  }
}
const keyColumns: Column[] = [
  { key: 'name', label: '名称', sortable: false },
  { key: 'key', label: 'API Key', sortable: false },
  { key: 'platform', label: '平台', sortable: false },
  { key: 'binding', label: '账号绑定', sortable: false },
  { key: 'priority', label: '调度优先级', sortable: false },
  { key: 'status', label: '状态', sortable: false },
  { key: 'created_at', label: '创建时间', sortable: false },
  { key: 'actions', label: '操作', sortable: false }
]
const copiedKeyId = ref<number | null>(null)
const publicSettings = ref<PublicSettings | null>(null)
const showUseKeyModal = ref(false)
const usingKey = ref<SharedAPIKey | null>(null)
const showCcsClientSelect = ref(false)
const pendingCcsKey = ref<SharedAPIKey | null>(null)
const canSaveKey = computed(() => !!keyName.value.trim() && (keySelection.value === 'platform' || keyListings.value.length > 0))
watch(keySelection, (mode) => {
  if (mode === 'platform') {
    keyListings.value = []
    if (keyPriority.value === 'order') keyPriority.value = 'rate'
  }
})
watch(keyPlatform, () => { keyListings.value = [] })
function keyPayload() {
  return {
    name: keyName.value.trim(),
    platform: keyPlatform.value,
    selection_mode: keySelection.value,
    priority_mode: keyPriority.value,
    listing_ids: keySelection.value === 'platform' ? [] : keyListings.value
  }
}
function openKeyModal() {
  editingKeyId.value = null
  keyMessage.value = ''
  keyName.value = ''
  keyPlatform.value = 'openai'
  keySelection.value = 'manual'
  keyPriority.value = 'order'
  keyListings.value = []
  showKeyModal.value = true
  void loadMyCards()
}
function openEditKey(key: SharedAPIKey) {
  editingKeyId.value = key.id
  keyMessage.value = ''
  keyName.value = key.name
  keyPlatform.value = key.platform
  keySelection.value = key.selection_mode === 'platform' ? 'platform' : 'manual'
  keyPriority.value = key.priority_mode === 'rate' || key.priority_mode === 'availability' ? key.priority_mode : (key.selection_mode === 'platform' ? 'rate' : 'order')
  keyListings.value = [...(key.listing_ids || [])]
  showKeyModal.value = true
}
function closeKeyModal() { if (!creatingKey.value) showKeyModal.value = false }
function toggleListing(id: number) { keyListings.value = keyListings.value.includes(id) ? keyListings.value.filter(value => value !== id) : [...keyListings.value, id] }
function moveListing(index: number, delta: number) { const next = index + delta; if (next < 0 || next >= keyListings.value.length) return; const ids = [...keyListings.value]; [ids[index], ids[next]] = [ids[next], ids[index]]; keyListings.value = ids }
function dragListing(event: DragEvent, index: number) { event.dataTransfer?.setData('text/plain', String(index)) }
function dropListing(event: DragEvent, target: number) { const source = Number(event.dataTransfer?.getData('text/plain')); if (!Number.isInteger(source) || source === target) return; const ids = [...keyListings.value]; const [id] = ids.splice(source, 1); ids.splice(target, 0, id); keyListings.value = ids }
function keyValue(key: SharedAPIKey) {
  return key.key || key.key_preview
}
function keyPlatformOf(key: SharedAPIKey): GroupPlatform {
  return (key.platform || 'anthropic') as GroupPlatform
}
function openUseKey(key: SharedAPIKey) {
  usingKey.value = key
  showUseKeyModal.value = true
}
function closeUseKey() {
  showUseKeyModal.value = false
  usingKey.value = null
}
function importToCcswitch(key: SharedAPIKey) {
  if (keyPlatformOf(key) === 'antigravity') {
    pendingCcsKey.value = key
    showCcsClientSelect.value = true
    return
  }
  executeCcsImport(key, keyPlatformOf(key) === 'gemini' ? 'gemini' : 'claude')
}
function executeCcsImport(key: SharedAPIKey, clientType: CcSwitchClientType) {
  const apiKey = keyValue(key)
  if (!apiKey) {
    keyMessage.value = '无法读取完整 Key，请刷新后重试'
    return
  }
  const baseUrl = publicSettings.value?.api_base_url || window.location.origin
  const usageScript = `({
    request: {
      url: "{{baseUrl}}/v1/usage",
      method: "GET",
      headers: { "Authorization": "Bearer {{apiKey}}" }
    },
    extractor: function(response) {
      const remaining = response?.remaining ?? response?.quota?.remaining ?? response?.balance;
      const unit = response?.unit ?? response?.quota?.unit ?? "USD";
      return {
        isValid: response?.is_active ?? response?.isValid ?? true,
        remaining,
        unit
      };
    }
  })`
  const deeplink = buildCcSwitchImportDeeplink({
    baseUrl,
    platform: keyPlatformOf(key),
    clientType,
    providerName: (publicSettings.value?.site_name || 'sub2api').trim() || 'sub2api',
    apiKey,
    usageScript
  })
  try {
    window.open(deeplink, '_self')
    setTimeout(() => {
      if (document.hasFocus()) keyMessage.value = '未检测到 CCS，请确认已安装 CC Switch'
    }, 100)
  } catch {
    keyMessage.value = '未检测到 CCS，请确认已安装 CC Switch'
  }
}
function handleCcsClientSelect(clientType: CcSwitchClientType) {
  if (pendingCcsKey.value) executeCcsImport(pendingCcsKey.value, clientType)
  showCcsClientSelect.value = false
  pendingCcsKey.value = null
}
function closeCcsClientSelect() {
  showCcsClientSelect.value = false
  pendingCcsKey.value = null
}
async function copyKeyValue(key: SharedAPIKey) {
  const value = keyValue(key)
  if (!value) return
  await navigator.clipboard.writeText(value)
  copiedKeyId.value = key.id
  setTimeout(() => { if (copiedKeyId.value === key.id) copiedKeyId.value = null }, 1500)
}
function bindingText(key: SharedAPIKey) {
  if (key.selection_mode === 'platform' || !key.listing_ids?.length) return '当前平台全部账号'
  return `${key.listing_ids.length} 个指定账号`
}
function priorityText(key: SharedAPIKey) {
  if (key.priority_mode === 'rate') return '倍率优先'
  if (key.priority_mode === 'availability') return '可用性优先'
  return '配置顺序'
}
async function createKey(){
  if (!canSaveKey.value || creatingKey.value) return
  creatingKey.value = true
  keyMessage.value = ''
  try {
    if (editingKeyId.value) {
      await updateSharedAPIKey(editingKeyId.value, keyPayload())
    } else {
      await createSharedAPIKey(keyPayload())
    }
    showKeyModal.value = false
    await loadKeys()
  } catch (e) { keyMessage.value = errorText(e) }
  finally { creatingKey.value = false }
}
async function removeKey(id:number){ if(!window.confirm('确定删除此共享 API Key 吗？')) return; await deleteSharedAPIKey(id); await loadKeys() }
async function toggleKey(key: SharedAPIKey) {
  await updateSharedAPIKey(key.id, {
    name: key.name,
    platform: key.platform,
    status: key.status === 'active' ? 'disabled' : 'active',
    selection_mode: key.selection_mode === 'platform' ? 'platform' : 'manual',
    priority_mode: key.priority_mode === 'rate' || key.priority_mode === 'availability' ? key.priority_mode : 'order',
    listing_ids: key.listing_ids || []
  })
  await loadKeys()
}
onMounted(() => { void loadKeys() })
</script>

<template>
  <AppLayout>
    <div class="space-y-6">
      <header class="flex flex-wrap items-center justify-between gap-4">
        <div>
          <h1 class="text-2xl font-semibold tracking-tight text-gray-900 dark:text-white">共享账号池</h1>
          <p class="mt-1 max-w-2xl text-sm leading-6 text-gray-500">上传后立即上线。用户可通过共享 API Key 按顺序调用，收益实时结算，可随时转入平台余额。</p>
        </div>
        <div class="flex gap-2">
          <button v-if="mode !== 'keys'" class="btn btn-primary" @click="openCreate">上传账号</button>
          <button v-if="mode !== 'keys'" class="btn btn-secondary" :disabled="loading" @click="loadCards">刷新账号</button>
        </div>
      </header>

      <div class="flex flex-wrap gap-2" aria-label="共享池模块">
        <button class="btn" :class="mode === 'pool' ? 'btn-primary' : 'btn-secondary'" @click="changeMode('pool')">共享池</button>
        <button class="btn" :class="mode === 'mine' ? 'btn-primary' : 'btn-secondary'" @click="changeMode('mine')">我的账号</button>
        <button class="btn" :class="mode === 'keys' ? 'btn-primary' : 'btn-secondary'" @click="changeMode('keys')">共享 API Key</button>
        <a class="btn btn-secondary" href="/usage">使用记录</a>
      </div>

      <section v-if="mode === 'keys'" aria-label="共享 API Key">
        <div class="mb-4 flex flex-wrap items-center justify-between gap-3">
          <div>
            <h2 class="font-semibold text-gray-900 dark:text-white">共享 API Key</h2>
            <p class="mt-1 text-xs text-gray-500">独立于普通 API Key。可指定账号并按顺序切换，或只选平台后按倍率 / 可用性调度。</p>
          </div>
          <div class="flex gap-2">
            <button class="btn btn-secondary" :disabled="loading" @click="loadKeys">
              <Icon name="refresh" size="md" />
            </button>
            <button class="btn btn-primary" @click="openKeyModal">
              <Icon name="plus" size="md" class="mr-2" />
              创建 Key
            </button>
          </div>
        </div>
        <div v-if="keyMessage" class="mb-4 rounded-lg bg-red-50 px-3 py-2 text-sm text-red-600 dark:bg-red-900/20 dark:text-red-300">{{ keyMessage }}</div>
        <div class="card overflow-hidden">
          <DataTable :columns="keyColumns" :data="sharedKeys" row-key="id">
            <template #cell-name="{ value }">
              <span class="font-medium text-gray-900 dark:text-white">{{ value }}</span>
            </template>
            <template #cell-key="{ row }">
              <div class="flex items-center gap-2">
                <code class="code text-xs">{{ maskApiKey(keyValue(row)) }}</code>
                <button class="rounded-lg p-1 text-gray-400 hover:bg-gray-100 hover:text-gray-600 dark:hover:bg-dark-700" :title="copiedKeyId === row.id ? '已复制' : '复制'" @click="copyKeyValue(row)">
                  <Icon v-if="copiedKeyId === row.id" name="check" size="sm" />
                  <Icon v-else name="clipboard" size="sm" />
                </button>
              </div>
            </template>
            <template #cell-platform="{ value }">
              <span class="inline-flex items-center gap-1.5 text-sm text-gray-700 dark:text-gray-300">
                <PlatformIcon :platform="value" size="xs" />
                {{ value }}
              </span>
            </template>
            <template #cell-binding="{ row }">
              <span class="text-sm text-gray-600 dark:text-gray-300">{{ bindingText(row) }}</span>
            </template>
            <template #cell-priority="{ row }">
              <span class="text-sm text-gray-600 dark:text-gray-300">{{ priorityText(row) }}</span>
            </template>
            <template #cell-status="{ value }">
              <span class="badge" :class="value === 'active' ? 'badge-success' : 'badge-gray'">{{ value === 'active' ? '启用' : '禁用' }}</span>
            </template>
            <template #cell-created_at="{ value }">
              <span class="text-sm text-gray-500">{{ timeText(value) }}</span>
            </template>
            <template #cell-actions="{ row }">
              <div class="flex items-center gap-1">
                <button class="flex flex-col items-center gap-0.5 rounded-lg p-1.5 text-gray-500 hover:bg-green-50 hover:text-green-600 dark:hover:bg-green-900/20 dark:hover:text-green-400" @click="openUseKey(row)">
                  <Icon name="terminal" size="sm" />
                  <span class="text-xs">使用密钥</span>
                </button>
                <button v-if="!publicSettings?.hide_ccs_import_button" class="flex flex-col items-center gap-0.5 rounded-lg p-1.5 text-gray-500 hover:bg-blue-50 hover:text-blue-600 dark:hover:bg-blue-900/20 dark:hover:text-blue-400" @click="importToCcswitch(row)">
                  <Icon name="upload" size="sm" />
                  <span class="text-xs">导入 CCS</span>
                </button>
                <button class="flex flex-col items-center gap-0.5 rounded-lg p-1.5 text-gray-500 hover:bg-gray-100 hover:text-primary-600 dark:hover:bg-dark-700" @click="openEditKey(row)">
                  <Icon name="edit" size="sm" />
                  <span class="text-xs">编辑</span>
                </button>
                <button class="flex flex-col items-center gap-0.5 rounded-lg p-1.5 text-gray-500" :class="row.status === 'active' ? 'hover:bg-yellow-50 hover:text-yellow-600' : 'hover:bg-green-50 hover:text-green-600'" @click="toggleKey(row)">
                  <Icon :name="row.status === 'active' ? 'ban' : 'checkCircle'" size="sm" />
                  <span class="text-xs">{{ row.status === 'active' ? '禁用' : '启用' }}</span>
                </button>
                <button class="flex flex-col items-center gap-0.5 rounded-lg p-1.5 text-gray-500 hover:bg-red-50 hover:text-red-600 dark:hover:bg-red-900/20" @click="removeKey(row.id)">
                  <Icon name="trash" size="sm" />
                  <span class="text-xs">删除</span>
                </button>
              </div>
            </template>
            <template #empty>
              <EmptyState title="还没有共享 Key" description="创建一个共享 API Key，绑定平台或指定账号。" action-text="创建 Key" @action="openKeyModal" />
            </template>
          </DataTable>
        </div>
      </section>

      <CreateAccountModal
        :show="showCreateAccount"
        :shared-pool="true"
        :proxies="[]"
        :groups="[]"
        @close="showCreateAccount = false"
        @created="loadCards"
      />
      <EditAccountModal
        :show="showEditAccount"
        :shared-pool="true"
        :account="editingAccount"
        :proxies="[]"
        :groups="[]"
        @close="showEditAccount = false; editingAccount = null"
        @updated="loadCards"
      />
      <ReAuthAccountModal
        :show="showReAuth"
        :shared-pool="true"
        :account="reAuthAccount"
        @close="showReAuth = false; reAuthAccount = null"
        @reauthorized="loadCards"
      />
      <UseKeyModal
        :show="showUseKeyModal"
        :api-key="usingKey ? keyValue(usingKey) : ''"
        :base-url="publicSettings?.api_base_url || ''"
        :platform="usingKey ? keyPlatformOf(usingKey) : null"
        :allow-messages-dispatch="false"
        @close="closeUseKey"
      />
      <BaseDialog :show="showCcsClientSelect" title="选择客户端" width="narrow" @close="closeCcsClientSelect">
        <p class="text-sm text-gray-600 dark:text-gray-400">请选择要导入到 CCS 的客户端</p>
        <div class="mt-4 grid grid-cols-2 gap-3">
          <button type="button" class="flex flex-col items-center gap-2 rounded-xl border-2 border-gray-200 p-4 transition-all hover:border-primary-500 hover:bg-primary-50 dark:border-dark-600 dark:hover:bg-primary-900/20" @click="handleCcsClientSelect('claude')">
            <Icon name="terminal" size="xl" class="text-gray-600 dark:text-gray-400" />
            <span class="font-medium text-gray-900 dark:text-white">Claude Code</span>
          </button>
          <button type="button" class="flex flex-col items-center gap-2 rounded-xl border-2 border-gray-200 p-4 transition-all hover:border-primary-500 hover:bg-primary-50 dark:border-dark-600 dark:hover:bg-primary-900/20" @click="handleCcsClientSelect('gemini')">
            <Icon name="sparkles" size="xl" class="text-gray-600 dark:text-gray-400" />
            <span class="font-medium text-gray-900 dark:text-white">Gemini CLI</span>
          </button>
        </div>
        <template #footer>
          <div class="flex justify-end">
            <button class="btn btn-secondary" type="button" @click="closeCcsClientSelect">取消</button>
          </div>
        </template>
      </BaseDialog>

      <section v-if="mode !== 'keys'" class="card p-5 sm:p-6" aria-label="共享收益">
        <div class="flex flex-wrap items-center justify-between gap-4">
          <h2 class="text-sm font-semibold text-gray-900 dark:text-white">我的共享收益</h2>
          <button class="btn btn-primary" :disabled="transferring || !wallet || Number(wallet.available) <= 0" @click="transfer">
            {{ transferring ? '转入中…' : '全部转入平台余额' }}
          </button>
        </div>
        <div v-if="wallet" class="mt-4 grid grid-cols-2 gap-4 lg:grid-cols-3">
          <div>
            <p class="text-xs text-gray-500">可提取</p>
            <p class="mt-1 text-2xl font-semibold tabular-nums text-emerald-600">{{ wallet.available }}</p>
          </div>
          <div>
            <p class="text-xs text-gray-500">累计收益</p>
            <p class="mt-1 text-xl font-medium tabular-nums text-gray-900 dark:text-white">{{ wallet.total_earned }}</p>
          </div>
          <div>
            <p class="text-xs text-gray-500">已转入余额</p>
            <p class="mt-1 text-xl font-medium tabular-nums text-gray-900 dark:text-white">{{ wallet.total_transferred }}</p>
          </div>
        </div>
        <p v-if="message" role="status" class="mt-3 text-sm text-emerald-600">{{ message }}</p>
        <div v-if="walletError" role="alert" class="mt-3 text-sm text-red-500">{{ walletError }} <button class="underline" @click="loadWallet">刷新钱包</button></div>
      </section>

      <div v-if="mode === 'pool' && !loading && platforms.length > 1" class="flex flex-wrap gap-2">
        <button class="rounded-full px-3 py-1 text-xs font-medium" :class="platformFilter === '' ? 'bg-gray-900 text-white dark:bg-white dark:text-gray-900' : 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-gray-300'" @click="platformFilter = ''">全部</button>
        <button v-for="platform in platforms" :key="platform" class="inline-flex items-center gap-1.5 rounded-full px-3 py-1 text-xs font-medium" :class="platformFilter === platform ? 'bg-gray-900 text-white dark:bg-white dark:text-gray-900' : 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-gray-300'" @click="platformFilter = platform">
          <PlatformIcon :platform="iconPlatform(platform)" size="xs" />
          {{ platform }}
        </button>
      </div>

      <div v-if="mode !== 'keys' && loading" class="py-16 text-center text-sm text-gray-500" role="status">加载中…</div>
      <div v-else-if="mode !== 'keys' && error" role="alert" class="card p-6 text-red-500">{{ error }} <button class="underline" @click="loadCards">重试</button></div>
      <div v-else-if="mode !== 'keys' && !cards.length" class="card p-10 text-center text-sm text-gray-500">{{ mode === 'mine' ? '你还没有共享账号' : '暂无可用共享账号' }}</div>
      <div v-else-if="mode === 'mine'" class="card overflow-x-auto">
        <div v-if="selectedCards.length" class="flex flex-wrap items-center gap-3 rounded-t-2xl bg-primary-50 px-5 py-3 text-sm text-primary-700 dark:bg-primary-900/20 dark:text-primary-300">
          <span>已选择 {{ selectedCards.length }} 个</span>
          <button class="font-medium underline" :disabled="batchBusy" @click="batchStatus('pause')">批量暂停</button>
          <button class="font-medium underline" :disabled="batchBusy" @click="batchStatus('resume')">批量恢复</button>
          <button class="font-medium underline text-red-600" :disabled="batchBusy" @click="batchDelete">批量删除</button>
          <button class="font-medium underline" @click="selectedCards = []">清除选择</button>
        </div>
        <table class="w-full min-w-[860px] text-left text-sm">
          <thead class="border-b border-gray-100 text-xs font-medium uppercase tracking-wide text-gray-400 dark:border-dark-700">
            <tr>
              <th class="w-12 px-5 py-3">
                <input type="checkbox" class="rounded border-gray-300 text-primary-600" :checked="selectedCards.length === cards.length && cards.length > 0" @change="selectedCards = ($event.target as HTMLInputElement).checked ? cards.map(card => card.id) : []" />
              </th>
              <th class="px-5 py-3">名称</th>
              <th class="px-5 py-3">平台 / 类型</th>
              <th class="px-5 py-3">最近请求</th>
              <th class="px-5 py-3">状态</th>
              <th class="px-5 py-3">容量</th>
              <th class="px-5 py-3">调度</th>
              <th class="px-5 py-3">公共池</th>
              <th class="px-5 py-3">倍率</th>
              <th class="px-5 py-3">最近调用</th>
              <th class="px-5 py-3">操作</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
            <tr v-for="card in cards" :key="card.id" class="hover:bg-gray-50 dark:hover:bg-dark-800">
              <td class="px-5 py-4">
                <input type="checkbox" class="rounded border-gray-300 text-primary-600" :checked="selectedCards.includes(card.id)" @change="selectedCards = ($event.target as HTMLInputElement).checked ? [...selectedCards, card.id] : selectedCards.filter(id => id !== card.id)" />
              </td>
              <td class="px-5 py-4">
                <div class="flex min-w-0 flex-col">
                  <span class="font-medium text-gray-900 dark:text-white">{{ card.display_name }}</span>
                  <span class="text-xs text-gray-500">并发 {{ card.concurrency_limit }}</span>
                </div>
              </td>
              <td class="px-5 py-4">
                <PlatformTypeBadge :platform="(card.platform || 'openai') as AccountPlatform" :type="(card.type || 'oauth') as AccountType" />
              </td>
              <td class="px-5 py-4">
                <RecentRequestsCell :requests="recentRequestsOf(card)" :interactive="false" />
              </td>
              <td class="px-5 py-4">
                <span class="rounded-full px-2.5 py-1 text-xs font-medium" :class="statusClass(card.status)">{{ labels[card.status] || card.status }}</span>
              </td>
              <td class="px-5 py-4">
                <CapacityBadge :color-class="capacityClass(card.current_concurrency || 0, card.concurrency_limit)" :current="card.current_concurrency || 0" :max="card.concurrency_limit">
                  <svg class="h-2.5 w-2.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                    <path stroke-linecap="round" stroke-linejoin="round" d="M3.75 6A2.25 2.25 0 016 3.75h2.25A2.25 2.25 0 0110.5 6v2.25a2.25 2.25 0 01-2.25 2.25H6a2.25 2.25 0 01-2.25-2.25V6zM3.75 15.75A2.25 2.25 0 016 13.5h2.25a2.25 2.25 0 012.25 2.25V18a2.25 2.25 0 01-2.25 2.25H6A2.25 2.25 0 013.75 18v-2.25zM13.5 6a2.25 2.25 0 012.25-2.25H18A2.25 2.25 0 0120.25 6v2.25A2.25 2.25 0 0118 10.5h-2.25a2.25 2.25 0 01-2.25-2.25V6zM13.5 15.75A2.25 2.25 0 0115.75 13.5H18a2.25 2.25 0 012.25 2.25V18A2.25 2.25 0 0118 20.25h-2.25A2.25 2.25 0 0113.5 18v-2.25z" />
                  </svg>
                </CapacityBadge>
              </td>
              <td class="px-5 py-4">
                <button role="switch" :aria-checked="card.status === 'active'" class="relative inline-flex h-5 w-9 flex-shrink-0 cursor-pointer rounded-full border-2 border-transparent" :class="card.status === 'active' ? 'bg-primary-500' : 'bg-gray-200 dark:bg-dark-600'" @click="toggle(card)">
                  <span class="pointer-events-none inline-block h-4 w-4 transform rounded-full bg-white shadow" :class="card.status === 'active' ? 'translate-x-4' : 'translate-x-0'" />
                </button>
              </td>
              <td class="px-5 py-4">
                <button role="switch" :aria-checked="card.listed !== false" class="relative inline-flex h-5 w-9 flex-shrink-0 cursor-pointer rounded-full border-2 border-transparent" :class="card.listed !== false ? 'bg-primary-500' : 'bg-gray-200 dark:bg-dark-600'" @click="toggleListed(card)">
                  <span class="pointer-events-none inline-block h-4 w-4 transform rounded-full bg-white shadow" :class="card.listed !== false ? 'translate-x-4' : 'translate-x-0'" />
                </button>
              </td>
              <td class="px-5 py-4 font-mono text-sm text-gray-700 dark:text-gray-300">{{ card.sell_rate }}x</td>
              <td class="px-5 py-4 text-sm text-gray-500">{{ timeText(card.last_called_at) }}</td>
              <td class="px-5 py-4">
                <div class="flex items-center gap-1">
                  <button class="flex flex-col items-center gap-0.5 rounded-lg p-1.5 text-gray-500 transition-colors hover:bg-gray-100 hover:text-primary-600 dark:hover:bg-dark-700 dark:hover:text-primary-400" @click="openEdit(card)">
                    <svg class="h-4 w-4" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="1.5"><path stroke-linecap="round" stroke-linejoin="round" d="M16.862 4.487l1.687-1.688a1.875 1.875 0 112.652 2.652L10.582 16.07a4.5 4.5 0 01-1.897 1.13L6 18l.8-2.685a4.5 4.5 0 011.13-1.897l8.932-8.931zm0 0L19.5 7.125M18 14v4.75A2.25 2.25 0 0115.75 21H5.25A2.25 2.25 0 013 18.75V8.25A2.25 2.25 0 015.25 6H10" /></svg>
                    <span class="text-xs">编辑</span>
                  </button>
                  <button class="flex flex-col items-center gap-0.5 rounded-lg p-1.5 text-gray-500 transition-colors hover:bg-red-50 hover:text-red-600 dark:hover:bg-red-900/20 dark:hover:text-red-400" @click="remove(card)">
                    <svg class="h-4 w-4" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="1.5"><path stroke-linecap="round" stroke-linejoin="round" d="M14.74 9l-.346 9m-4.788 0L9.26 9m9.968-3.21c.342.052.682.107 1.022.166m-1.022-.165L18.16 19.673a2.25 2.25 0 01-2.244 2.077H8.084a2.25 2.25 0 01-2.244-2.077L4.772 5.79m14.456 0a48.108 48.108 0 00-3.478-.397m-12 .562c.34-.059.68-.114 1.022-.165m0 0a48.11 48.11 0 013.478-.397m7.5 0v-.916c0-1.18-.91-2.164-2.09-2.201a51.964 51.964 0 00-3.32 0c-1.18.037-2.09 1.022-2.09 2.201v.916m7.5 0a48.667 48.667 0 00-7.5 0" /></svg>
                    <span class="text-xs">删除</span>
                  </button>
                  <button class="flex flex-col items-center gap-0.5 rounded-lg p-1.5 text-gray-500 transition-colors hover:bg-gray-100 hover:text-gray-900 dark:hover:bg-dark-700 dark:hover:text-white" @click="openMenu(card, $event)">
                    <svg class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.5"><path stroke-linecap="round" stroke-linejoin="round" d="M6.75 12a.75.75 0 11-1.5 0 .75.75 0 011.5 0zM12.75 12a.75.75 0 11-1.5 0 .75.75 0 011.5 0zM18.75 12a.75.75 0 11-1.5 0 .75.75 0 011.5 0z" /></svg>
                    <span class="text-xs">更多</span>
                  </button>
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
      <div v-else-if="mode === 'pool'" class="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
        <article v-for="card in visibleCards" :key="card.id" class="card card-hover flex min-w-0 flex-col p-5">
          <div class="flex items-start justify-between gap-3">
            <div class="flex min-w-0 items-start gap-3">
              <span class="mt-0.5 flex h-9 w-9 shrink-0 items-center justify-center rounded-xl bg-gray-50 text-gray-600 dark:bg-dark-700 dark:text-gray-300">
                <PlatformIcon :platform="iconPlatform(card.platform)" size="sm" />
              </span>
              <div class="min-w-0">
                <h2 class="truncate text-base font-semibold text-gray-900 dark:text-white" :title="card.display_name">{{ card.display_name }}</h2>
                <p class="mt-0.5 truncate text-xs text-gray-500">
                  {{ card.platform }}
                  <span v-if="card.type"> · {{ typeText(card) }}</span>
                  <span v-if="card.uploader_name"> · {{ card.uploader_name }}</span>
                </p>
              </div>
            </div>
            <span class="shrink-0 rounded-full px-2.5 py-1 text-[11px] font-medium" :class="statusClass(card.status)">{{ labels[card.status] || card.status }}</span>
          </div>

          <dl class="mt-4 grid grid-cols-2 gap-3 text-sm">
            <div class="rounded-xl bg-gray-50 px-3 py-2.5 dark:bg-dark-800">
              <dt class="text-[11px] text-gray-400">累计调用</dt>
              <dd class="mt-1 text-lg font-semibold tabular-nums tracking-tight text-gray-900 dark:text-white">{{ card.total_call_count.toLocaleString() }}</dd>
            </div>
            <div class="rounded-xl bg-gray-50 px-3 py-2.5 dark:bg-dark-800">
              <dt class="text-[11px] text-gray-400">平均延迟</dt>
              <dd class="mt-1 text-lg font-semibold tabular-nums tracking-tight text-gray-900 dark:text-white">{{ avgLatency(card) ? `${avgLatency(card)}ms` : '—' }}</dd>
            </div>
            <div class="rounded-xl bg-gray-50 px-3 py-2.5 dark:bg-dark-800">
              <dt class="text-[11px] text-gray-400">容量</dt>
              <dd class="mt-1">
                <CapacityBadge :color-class="capacityClass(card.current_concurrency || 0, card.concurrency_limit)" :current="card.current_concurrency || 0" :max="card.concurrency_limit">
                  <svg class="h-2.5 w-2.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                    <path stroke-linecap="round" stroke-linejoin="round" d="M3.75 6A2.25 2.25 0 016 3.75h2.25A2.25 2.25 0 0110.5 6v2.25a2.25 2.25 0 01-2.25 2.25H6a2.25 2.25 0 01-2.25-2.25V6zM3.75 15.75A2.25 2.25 0 016 13.5h2.25a2.25 2.25 0 012.25 2.25V18a2.25 2.25 0 01-2.25 2.25H6A2.25 2.25 0 013.75 18v-2.25zM13.5 6a2.25 2.25 0 012.25-2.25H18A2.25 2.25 0 0120.25 6v2.25A2.25 2.25 0 0118 10.5h-2.25a2.25 2.25 0 01-2.25-2.25V6zM13.5 15.75A2.25 2.25 0 0115.75 13.5H18a2.25 2.25 0 012.25 2.25V18A2.25 2.25 0 0118 20.25h-2.25A2.25 2.25 0 0113.5 18v-2.25z" />
                  </svg>
                </CapacityBadge>
              </dd>
            </div>
            <div class="rounded-xl bg-gray-50 px-3 py-2.5 dark:bg-dark-800">
              <dt class="text-[11px] text-gray-400">售价倍率</dt>
              <dd class="mt-1 text-sm font-medium tabular-nums text-gray-800 dark:text-gray-100">{{ card.sell_rate }}x</dd>
            </div>
          </dl>

          <div class="mt-4">
            <div class="mb-1.5 flex items-center justify-between">
              <p class="text-[11px] font-medium uppercase tracking-wide text-gray-400">可用模型</p>
              <span class="text-[11px] tabular-nums text-gray-400">{{ modelsFor(card).length }}</span>
            </div>
            <div class="flex flex-wrap gap-1">
              <span v-for="model in modelsFor(card)" :key="model" class="max-w-full truncate rounded-md bg-gray-100 px-1.5 py-0.5 font-mono text-[10px] leading-4 text-gray-600 dark:bg-dark-700 dark:text-gray-300" :title="model">{{ model }}</span>
            </div>
          </div>

          <div class="mt-4 border-t border-gray-100 pt-3 dark:border-dark-700">
            <p class="mb-2 text-[11px] font-medium uppercase tracking-wide text-gray-400">最近请求</p>
            <RecentRequestsCell :requests="recentRequestsOf(card)" :interactive="false" />
          </div>
        </article>
      </div>
    </div>
    <Teleport to="body">
      <div v-if="menu.show && menu.card && menu.anchorRect">
        <div class="fixed inset-0 z-[9998]" @click="closeMenu"></div>
        <div class="fixed z-[9999] w-52 overflow-hidden rounded-xl bg-white py-1 shadow-lg ring-1 ring-black/5 dark:bg-dark-800" :style="{ top: `${menu.anchorRect.bottom + 4}px`, left: `${Math.max(8, menu.anchorRect.right - 208)}px` }">
          <button class="flex w-full items-center gap-2 px-4 py-2 text-sm hover:bg-gray-100 dark:hover:bg-dark-700" @click="openTest(menu.card); closeMenu()">测试连接</button>
          <button v-if="canReauth(menu.card)" class="flex w-full items-center gap-2 px-4 py-2 text-sm text-blue-600 hover:bg-gray-100 dark:hover:bg-dark-700" @click="openReAuth(menu.card); closeMenu()">重新授权</button>
        </div>
      </div>
    </Teleport>
    <AccountTestModal :show="!!testingCard" :account="testingCard" :shared-pool="true" @close="testingCard = null" />
    <BaseDialog :show="showKeyModal" :title="editingKeyId ? '编辑共享 API Key' : '创建共享 API Key'" width="normal" @close="closeKeyModal">
      <form class="space-y-5" @submit.prevent="createKey">
        <div>
          <label class="input-label">名称</label>
          <input v-model="keyName" class="input" maxlength="100" required placeholder="例如：共享 OpenAI" />
        </div>
        <div>
          <label class="input-label">平台</label>
          <select v-model="keyPlatform" class="input">
            <option value="openai">OpenAI</option>
            <option value="anthropic">Anthropic</option>
            <option value="gemini">Gemini</option>
            <option value="grok">Grok</option>
            <option value="antigravity">Antigravity</option>
          </select>
        </div>
        <div>
          <label class="input-label">账号绑定</label>
          <div class="mt-2 grid grid-cols-2 gap-2">
            <button type="button" class="rounded-xl border px-3 py-2 text-left text-sm" :class="keySelection === 'manual' ? 'border-primary-500 bg-primary-50 text-primary-700 dark:bg-primary-900/20' : 'border-gray-200 dark:border-dark-600'" @click="keySelection = 'manual'">
              <span class="block font-medium">指定账号</span>
              <span class="mt-0.5 block text-xs text-gray-500">按配置顺序故障切换</span>
            </button>
            <button type="button" class="rounded-xl border px-3 py-2 text-left text-sm" :class="keySelection === 'platform' ? 'border-primary-500 bg-primary-50 text-primary-700 dark:bg-primary-900/20' : 'border-gray-200 dark:border-dark-600'" @click="keySelection = 'platform'">
              <span class="block font-medium">仅选平台</span>
              <span class="mt-0.5 block text-xs text-gray-500">使用该平台全部可用账号</span>
            </button>
          </div>
        </div>
        <div>
          <label class="input-label">调度优先级</label>
          <div class="mt-2 grid grid-cols-1 gap-2 sm:grid-cols-3">
            <button type="button" :disabled="keySelection === 'platform'" class="rounded-xl border px-3 py-2 text-left text-sm disabled:cursor-not-allowed disabled:opacity-40" :class="keyPriority === 'order' ? 'border-primary-500 bg-primary-50 text-primary-700 dark:bg-primary-900/20' : 'border-gray-200 dark:border-dark-600'" @click="keyPriority = 'order'">
              <span class="block font-medium">配置顺序</span>
              <span class="mt-0.5 block text-xs text-gray-500">按下方列表依次使用</span>
            </button>
            <button type="button" class="rounded-xl border px-3 py-2 text-left text-sm" :class="keyPriority === 'rate' ? 'border-primary-500 bg-primary-50 text-primary-700 dark:bg-primary-900/20' : 'border-gray-200 dark:border-dark-600'" @click="keyPriority = 'rate'">
              <span class="block font-medium">倍率优先</span>
              <span class="mt-0.5 block text-xs text-gray-500">售价倍率低的账号优先</span>
            </button>
            <button type="button" class="rounded-xl border px-3 py-2 text-left text-sm" :class="keyPriority === 'availability' ? 'border-primary-500 bg-primary-50 text-primary-700 dark:bg-primary-900/20' : 'border-gray-200 dark:border-dark-600'" @click="keyPriority = 'availability'">
              <span class="block font-medium">可用性优先</span>
              <span class="mt-0.5 block text-xs text-gray-500">空闲、未限流的账号优先</span>
            </button>
          </div>
        </div>
        <div v-if="keySelection === 'manual'">
          <p class="mb-2 text-sm font-medium">选择共享账号（拖拽调整顺序）</p>
          <div class="max-h-52 space-y-2 overflow-y-auto rounded-lg border border-gray-200 p-2 dark:border-dark-700">
            <button v-for="card in availableKeyCards" :key="card.id" type="button" class="flex w-full items-center justify-between rounded px-3 py-2 text-left text-sm" :class="keyListings.includes(card.id) ? 'bg-primary-50 text-primary-700' : 'hover:bg-gray-50 dark:hover:bg-dark-800'" @click="toggleListing(card.id)">
              <span>{{ card.display_name }} <span class="text-xs text-gray-500">· 倍率 {{ card.sell_rate }}x · 并发 {{ card.concurrency_limit }}</span></span>
              <span>{{ keyListings.includes(card.id) ? '已选择' : '选择' }}</span>
            </button>
            <p v-if="!availableKeyCards.length" class="p-4 text-center text-xs text-gray-400">当前平台暂无可用共享账号</p>
          </div>
          <ol class="mt-3 space-y-2">
            <li v-for="(id, index) in keyListings" :key="id" draggable="true" class="flex items-center justify-between rounded-lg bg-gray-50 px-3 py-2 text-sm dark:bg-dark-800" @dragstart="dragListing($event, index)" @dragover.prevent @drop="dropListing($event, index)">
              <span>{{ index + 1 }}. {{ cards.find(card => card.id === id)?.display_name || `账号 ${id}` }}</span>
              <span class="flex gap-1">
                <button type="button" class="px-2" :disabled="index === 0" @click="moveListing(index, -1)">↑</button>
                <button type="button" class="px-2" :disabled="index === keyListings.length - 1" @click="moveListing(index, 1)">↓</button>
                <button type="button" class="px-2 text-red-500" @click="toggleListing(id)">×</button>
              </span>
            </li>
          </ol>
        </div>
        <p v-else class="rounded-lg bg-gray-50 px-3 py-2 text-xs text-gray-500 dark:bg-dark-800">不绑定具体账号。请求时使用当前平台所有可用共享账号，并按所选优先级调度。</p>
      </form>
      <template #footer>
        <div class="flex justify-end gap-2">
          <button class="btn btn-secondary" type="button" @click="closeKeyModal">取消</button>
          <button class="btn btn-primary" type="button" :disabled="creatingKey || !canSaveKey" @click="createKey">{{ creatingKey ? '保存中…' : (editingKeyId ? '保存' : '创建 Key') }}</button>
        </div>
      </template>
    </BaseDialog>
  </AppLayout>
</template>
