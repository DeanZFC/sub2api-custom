<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import AppLayout from '@/components/layout/AppLayout.vue'
import CreateAccountModal from '@/components/account/CreateAccountModal.vue'
import AccountTestModal from '@/components/admin/account/AccountTestModal.vue'
import {
  getSharedPoolCards, getMySharedCards, getSharedWallet, transferSharedEarnings, setSharedListingStatus, deleteSharedListing,
  listSharedAPIKeys, createSharedAPIKey, updateSharedAPIKey, deleteSharedAPIKey,
  type SharedCard, type SharedWallet, type SharedAPIKey
} from '@/api/sharedPool'

const cards = ref<SharedCard[]>([])
const wallet = ref<SharedWallet | null>(null)
const mode = ref<'pool' | 'mine' | 'keys'>('pool')
const loading = ref(true)
const error = ref('')
const walletError = ref('')
const transferring = ref(false)
const message = ref('')
const showCreateAccount = ref(false)
const testingCard = ref<any>(null)
const sharedKeys = ref<SharedAPIKey[]>([])
const keyName = ref(''); const keyPlatform = ref('openai'); const keyListings = ref<number[]>([]); const keyMessage = ref('')
const showKeyModal = ref(false)
const creatingKey = ref(false)
const editingKeyId = ref<number | null>(null)
const availableKeyCards = computed(() => cards.value.filter(card => card.status === 'active' && card.platform.toLowerCase() === keyPlatform.value.toLowerCase()))
let transferKey: string | null = null
let generation = 0

function errorText(error: unknown) {
  if (error instanceof Error) return error.message
  if (typeof error === 'object' && error !== null && 'message' in error) return String(error.message)
  return '请求失败，请重试'
}
async function loadCards() {
  const current = ++generation
  loading.value = true
  error.value = ''
  try {
    const items = mode.value === 'mine'
      ? await getMySharedCards()
      : await getSharedPoolCards({ limit: 100, recent_limit: 8 })
    if (current === generation) cards.value = items
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
  if (next === 'keys') { void loadCards(); void loadKeys() }
  else void loadCards()
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
async function toggle(card: SharedCard) {
  try { await setSharedListingStatus(card.id, card.status === 'paused' ? 'resume' : 'pause'); await loadCards() }
  catch (e) { error.value = errorText(e) }
}
async function remove(card: SharedCard) {
  if (!window.confirm('确定删除这个共享账号吗？')) return
  try { await deleteSharedListing(card.id); await loadCards() }
  catch (e) { error.value = errorText(e) }
}
const labels: Record<string, string> = {
  active: '运行中', paused: '已暂停', testing: '检测中', invalid: '不可用', suspended: '已停用'
}
const timeText = (value?: string) => value ? new Date(value).toLocaleString() : '暂无调用'
onMounted(() => { void loadCards(); void loadWallet() })
async function loadKeys(){ try { sharedKeys.value = await listSharedAPIKeys() } catch {} }
function openKeyModal() { editingKeyId.value = null; keyMessage.value = ''; keyName.value = ''; keyListings.value = []; showKeyModal.value = true }
function openTest(card: SharedCard) { testingCard.value = { id: card.id, name: card.display_name, type: 'shared', platform: card.platform, status: card.status } }
function openEditKey(key: SharedAPIKey) { editingKeyId.value = key.id; keyMessage.value = ''; keyName.value = key.name; keyPlatform.value = key.platform; keyListings.value = [...key.listing_ids]; showKeyModal.value = true }
function closeKeyModal() { if (!creatingKey.value) showKeyModal.value = false }
function toggleListing(id: number) { keyListings.value = keyListings.value.includes(id) ? keyListings.value.filter(value => value !== id) : [...keyListings.value, id] }
function moveListing(index: number, delta: number) { const next = index + delta; if (next < 0 || next >= keyListings.value.length) return; const ids = [...keyListings.value]; [ids[index], ids[next]] = [ids[next], ids[index]]; keyListings.value = ids }
function dragListing(event: DragEvent, index: number) { event.dataTransfer?.setData('text/plain', String(index)) }
function dropListing(event: DragEvent, target: number) { const source = Number(event.dataTransfer?.getData('text/plain')); if (!Number.isInteger(source) || source === target) return; const ids = [...keyListings.value]; const [id] = ids.splice(source, 1); ids.splice(target, 0, id); keyListings.value = ids }
async function createKey(){ if (!keyName.value.trim() || !keyListings.value.length || creatingKey.value) return; creatingKey.value = true; keyMessage.value=''; try { if (editingKeyId.value) { await updateSharedAPIKey(editingKeyId.value, {name:keyName.value.trim(),platform:keyPlatform.value,listing_ids:keyListings.value}); showKeyModal.value = false } else { const k=await createSharedAPIKey({name:keyName.value.trim(),platform:keyPlatform.value,listing_ids:keyListings.value}); keyMessage.value=`新 Key：${k.key}（仅显示一次，请立即复制保存）`; keyName.value=''; keyListings.value=[]; showKeyModal.value = false } await loadKeys() } catch(e){ keyMessage.value=errorText(e) } finally { creatingKey.value = false } }
async function removeKey(id:number){ if(!window.confirm('确定删除此共享 API Key 吗？')) return; await deleteSharedAPIKey(id); await loadKeys() }
async function toggleKey(key: SharedAPIKey) { await updateSharedAPIKey(key.id, { name: key.name, status: key.status === 'active' ? 'disabled' : 'active', listing_ids: key.listing_ids }); await loadKeys() }
onMounted(() => { void loadKeys() })
</script>

<template>
  <AppLayout>
    <div class="space-y-6">
      <header class="flex flex-wrap items-center justify-between gap-4">
        <div>
          <h1 class="text-2xl font-bold text-gray-900 dark:text-white">共享账号池</h1>
          <p class="mt-1 text-sm text-gray-500">上传后自动上线，无需管理员审核。查看可用账号、调用记录与共享收益。</p>
        </div>
        <div class="flex gap-2"><button v-if="mode !== 'keys'" class="btn btn-primary" @click="showCreateAccount = true">上传账号</button><button v-if="mode !== 'keys'" class="btn btn-secondary" :disabled="loading" @click="loadCards">刷新账号</button></div>
      </header>
      <p class="text-sm text-gray-500">共享账号上传后立即生效，用户可通过共享 API Key 按顺序调用；收益实时结算，可随时转入平台余额。</p>
      <div class="flex flex-wrap gap-2" aria-label="共享池模块">
        <button class="btn" :class="mode === 'pool' ? 'btn-primary' : 'btn-secondary'" @click="changeMode('pool')">共享池</button>
        <button class="btn" :class="mode === 'mine' ? 'btn-primary' : 'btn-secondary'" @click="changeMode('mine')">我的账号</button>
        <button class="btn" :class="mode === 'keys' ? 'btn-primary' : 'btn-secondary'" @click="changeMode('keys')">共享 API Key</button>
      </div>
      <section v-if="mode === 'keys'" class="card p-6" aria-label="共享 API Key">
        <div class="flex flex-wrap items-center justify-between gap-4"><div><h2 class="font-semibold text-gray-900 dark:text-white">共享 API Key</h2><p class="mt-1 text-xs text-gray-500">独立于普通 API Key，可绑定多个账号并按顺序故障切换。</p></div><button class="btn btn-primary" @click="openKeyModal">创建共享 Key</button></div>
        <p v-if="keyMessage" class="mt-4 rounded-lg bg-emerald-50 px-3 py-2 text-sm text-emerald-700">{{ keyMessage }}</p>
        <div v-if="!sharedKeys.length" class="mt-6 text-center text-sm text-gray-400">暂无共享 Key</div>
        <ul v-else class="mt-6 space-y-3"><li v-for="key in sharedKeys" :key="key.id" class="rounded-lg border border-gray-200 p-4 dark:border-dark-700"><div class="flex flex-wrap items-center justify-between gap-3"><div><p class="font-medium text-gray-900 dark:text-white">{{ key.name }}</p><p class="mt-1 text-xs text-gray-500">{{ key.platform }} · {{ key.key_preview }} · {{ key.listing_ids.length }} 个账号</p></div><div class="flex gap-2"><button class="btn btn-secondary btn-sm" @click="openEditKey(key)">编辑顺序</button><button class="btn btn-secondary btn-sm" @click="toggleKey(key)">{{ key.status === 'active' ? '禁用' : '启用' }}</button><button class="btn btn-secondary btn-sm text-red-500" @click="removeKey(key.id)">删除</button></div></div></li></ul>
      </section>
      <CreateAccountModal
        :show="showCreateAccount"
        :shared-pool="true"
        :proxies="[]"
        :groups="[]"
        @close="showCreateAccount = false"
        @created="loadCards"
      />

      <section v-if="mode !== 'keys'" class="card p-6" aria-label="共享收益">
        <div class="flex flex-wrap items-center justify-between gap-4">
          <h2 class="font-semibold text-gray-900 dark:text-white">我的共享收益</h2>
          <button class="btn btn-primary" :disabled="transferring || !wallet || Number(wallet.available) <= 0" @click="transfer">
            {{ transferring ? '转入中…' : '全部转入平台余额' }}
          </button>
        </div>
        <div v-if="wallet" class="mt-4 grid grid-cols-2 gap-4 lg:grid-cols-4">
          <div><p class="text-xs text-gray-500">可提取</p><p class="mt-1 text-2xl font-bold text-emerald-600">{{ wallet.available }}</p></div>
          <div><p class="text-xs text-gray-500">累计收益</p><p class="mt-1 text-xl text-gray-900 dark:text-white">{{ wallet.total_earned }}</p></div>
          <div><p class="text-xs text-gray-500">已转入余额</p><p class="mt-1 text-xl text-gray-900 dark:text-white">{{ wallet.total_transferred }}</p></div>
        </div>
        <p v-if="wallet && Number(wallet.frozen) > 0" class="mt-3 text-sm text-amber-600">冻结收益：{{ wallet.frozen }}</p>
        <p v-if="message" role="status" class="mt-3 text-sm text-emerald-600">{{ message }}</p>
        <div v-if="walletError" role="alert" class="mt-3 text-sm text-red-500">{{ walletError }} <button class="underline" @click="loadWallet">刷新钱包</button></div>
      </section>
      <div v-if="mode !== 'keys' && loading" class="py-16 text-center text-gray-500" role="status">加载中…</div>
      <div v-else-if="mode !== 'keys' && error" role="alert" class="card p-6 text-red-500">{{ error }} <button class="underline" @click="loadCards">重试</button></div>
      <div v-else-if="mode !== 'keys' && !cards.length" class="card p-10 text-center text-gray-500">{{ mode === 'mine' ? '你还没有共享账号' : '暂无可用共享账号' }}</div>
      <div v-else-if="mode !== 'keys'" class="grid gap-5 sm:grid-cols-2 xl:grid-cols-3">
        <article v-for="card in cards" :key="card.id" class="card min-w-0 overflow-hidden p-6">
          <div class="flex flex-col items-center gap-2">
            <div class="min-w-0"><h2 class="break-words text-xl font-semibold text-gray-900 dark:text-white">{{ card.display_name }}</h2><p class="mt-1 text-base text-gray-500">{{ card.platform }}</p></div>
            <span class="shrink-0 rounded-full px-3 py-1 text-xs font-medium" :class="card.status === 'active' ? 'bg-emerald-100 text-emerald-700' : 'bg-gray-100 text-gray-600'">{{ labels[card.status] || card.status }}</span>
          </div>
          <div class="mt-6"><p class="text-sm text-gray-500">累计调用</p><p class="mt-1 break-all text-5xl font-bold tracking-tight text-primary-600">{{ card.total_call_count.toLocaleString() }}</p></div>
          <div class="mt-4 flex flex-wrap justify-center gap-3 text-xs text-gray-500"><span>并发上限 {{ card.concurrency_limit }}</span><span>倍率 {{ card.sell_rate }}x</span></div>
          <p class="mt-3 text-xs text-gray-400">最近调用：{{ timeText(card.last_called_at) }}</p>
          <div v-if="mode === 'mine'" class="mt-3 flex items-center justify-center gap-3"><button class="btn btn-secondary btn-sm" @click="openTest(card)">测试连接</button><button role="switch" :aria-checked="card.status === 'active'" class="relative h-6 w-11 rounded-full transition-colors" :class="card.status === 'active' ? 'bg-primary-600' : 'bg-gray-300'" @click="toggle(card)"><span class="absolute top-1 h-4 w-4 rounded-full bg-white transition-transform" :class="card.status === 'active' ? 'translate-x-6' : 'translate-x-1'" /></button><button class="btn btn-secondary btn-sm" @click="showCreateAccount = true">编辑</button><button class="btn btn-secondary btn-sm text-red-500" @click="remove(card)">删除</button></div>
          <div class="mt-6 border-t border-gray-200 pt-4 text-left dark:border-dark-700">
            <div class="mb-3 flex items-center justify-between"><h3 class="text-sm font-semibold text-gray-900 dark:text-white">最近请求</h3><span class="text-xs text-gray-400">{{ card.recent_calls.length }} 条</span></div>
            <div v-if="!card.recent_calls.length" class="text-xs text-gray-400">暂无请求记录</div>
            <ol class="space-y-2">
              <li v-for="call in card.recent_calls" :key="call.request_id" class="rounded-lg bg-gray-50 px-3 py-2 text-xs dark:bg-dark-800">
                <div class="flex justify-between gap-2"><span class="truncate text-gray-700 dark:text-gray-300">{{ call.model || '未记录模型' }}</span><span class="shrink-0" :class="call.result_status === 'success' ? 'text-emerald-600' : 'text-red-500'">{{ call.result_status === 'success' ? '成功' : call.result_status }}</span></div>
                <div class="mt-1 flex justify-between gap-2 text-gray-400"><span class="max-w-[10rem] truncate" :title="call.request_id">请求 {{ call.request_id }}</span><time :datetime="call.created_at">{{ timeText(call.created_at) }}</time><span class="shrink-0">{{ call.duration_ms ?? 0 }}ms</span></div>
              </li>
            </ol>
          </div>
        </article>
      </div>
    </div>
    <AccountTestModal :show="!!testingCard" :account="testingCard" @close="testingCard = null" />
    <Teleport to="body"><div v-if="showKeyModal" class="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4" role="dialog" aria-modal="true" :aria-label="editingKeyId ? '编辑共享 API Key' : '创建共享 API Key'" @click.self="closeKeyModal"><div class="w-full max-w-xl rounded-2xl bg-white p-6 shadow-xl dark:bg-dark-900"><div class="flex items-center justify-between"><h2 class="text-lg font-semibold">{{ editingKeyId ? '编辑共享 API Key' : '创建共享 API Key' }}</h2><button class="text-gray-400" @click="closeKeyModal">×</button></div><div class="mt-5 space-y-4"><input v-model="keyName" class="input w-full" placeholder="Key 名称" maxlength="100" /><select v-model="keyPlatform" class="input w-full" @change="keyListings = []"><option value="openai">OpenAI</option><option value="anthropic">Anthropic</option><option value="gemini">Gemini</option><option value="grok">Grok</option></select><div><p class="mb-2 text-sm font-medium">选择共享账号（拖拽调整顺序）</p><div class="max-h-52 space-y-2 overflow-y-auto rounded-lg border border-gray-200 p-2 dark:border-dark-700"><button v-for="card in availableKeyCards" :key="card.id" type="button" class="flex w-full items-center justify-between rounded px-3 py-2 text-left text-sm" :class="keyListings.includes(card.id) ? 'bg-primary-50 text-primary-700' : 'hover:bg-gray-50 dark:hover:bg-dark-800'" @click="toggleListing(card.id)"><span>{{ card.display_name }}</span><span>{{ keyListings.includes(card.id) ? '已选择' : '选择' }}</span></button><p v-if="!availableKeyCards.length" class="p-4 text-center text-xs text-gray-400">当前平台暂无可用共享账号</p></div></div><ol class="space-y-2"><li v-for="(id, index) in keyListings" :key="id" draggable="true" class="flex items-center justify-between rounded-lg bg-gray-50 px-3 py-2 text-sm dark:bg-dark-800" @dragstart="dragListing($event, index)" @dragover.prevent @drop="dropListing($event, index)"><span>{{ index + 1 }}. {{ cards.find(card => card.id === id)?.display_name || `账号 ${id}` }}</span><span class="flex gap-1"><button type="button" class="px-2" :disabled="index === 0" @click="moveListing(index, -1)">↑</button><button type="button" class="px-2" :disabled="index === keyListings.length - 1" @click="moveListing(index, 1)">↓</button><button type="button" class="px-2 text-red-500" @click="toggleListing(id)">×</button></span></li></ol></div><div class="mt-6 flex justify-end gap-2"><button class="btn btn-secondary" @click="closeKeyModal">取消</button><button class="btn btn-primary" :disabled="creatingKey || !keyName.trim() || !keyListings.length" @click="createKey">{{ creatingKey ? '创建中…' : '创建 Key' }}</button></div></div></div></Teleport>
  </AppLayout>
</template>
