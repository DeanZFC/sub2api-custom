<script setup lang="ts">
import { onMounted, ref } from 'vue'
import AppLayout from '@/components/layout/AppLayout.vue'
import {
  getSharedPoolCards, getMySharedCards, getSharedWallet, transferSharedEarnings, createSharedListing, setSharedListingStatus, deleteSharedListing,
  type SharedCard, type SharedWallet
} from '@/api/sharedPool'

const cards = ref<SharedCard[]>([])
const wallet = ref<SharedWallet | null>(null)
const mode = ref<'pool' | 'mine'>('pool')
const loading = ref(true)
const error = ref('')
const walletError = ref('')
const transferring = ref(false)
const message = ref('')
const showUpload = ref(false)
const uploading = ref(false)
const uploadError = ref('')
const upload = ref({ name: '', platform: 'openai', type: 'apikey', credentials: '{}', proxy_url: '', concurrency: 3, concurrency_multiplier: 1, sell_rate: 1 })
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
function changeMode(next: 'pool' | 'mine') {
  mode.value = next
  void loadCards()
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
async function submitUpload() {
  uploadError.value = ''
  let credentials: Record<string, unknown>
  try { credentials = JSON.parse(upload.value.credentials) } catch { uploadError.value = '凭证必须是有效 JSON'; return }
  uploading.value = true
  try {
    await createSharedListing({ ...upload.value, credentials })
    showUpload.value = false; upload.value = { name: '', platform: 'openai', type: 'apikey', credentials: '{}', proxy_url: '', concurrency: 3, concurrency_multiplier: 1, sell_rate: 1 }
    mode.value = 'mine'; await loadCards()
  } catch (e) { uploadError.value = errorText(e) } finally { uploading.value = false }
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
</script>

<template>
  <AppLayout>
    <div class="space-y-6">
      <header class="flex flex-wrap items-center justify-between gap-4">
        <div>
          <h1 class="text-2xl font-bold text-gray-900 dark:text-white">共享账号池</h1>
          <p class="mt-1 text-sm text-gray-500">上传后自动上线，无需管理员审核。查看可用账号、调用记录与共享收益。</p>
        </div>
        <div class="flex gap-2"><button class="btn btn-primary" @click="showUpload = true">上传账号</button><button class="btn btn-secondary" :disabled="loading" @click="loadCards">刷新账号</button></div>
      </header>
      <p class="text-sm text-gray-500">使用共享账号：在 API 密钥页面选择 shared- 对应平台分组。共享消费从平台余额扣除；共享收益结算 48 小时后可手动转入余额。</p>
      <section v-if="showUpload" class="card p-6">
        <div class="flex items-center justify-between"><h2 class="font-semibold text-gray-900 dark:text-white">上传共享账号</h2><button class="text-gray-400" @click="showUpload = false">关闭</button></div>
        <form class="mt-4 grid gap-4 md:grid-cols-2" @submit.prevent="submitUpload">
          <label class="text-sm">展示名称<input v-model="upload.name" required class="input mt-1 w-full" maxlength="100" /></label>
          <label class="text-sm">平台<select v-model="upload.platform" class="input mt-1 w-full"><option>openai</option><option value="anthropic">Claude</option><option>gemini</option><option>antigravity</option><option>grok</option></select></label>
          <label class="text-sm">认证类型<input v-model="upload.type" required class="input mt-1 w-full" /></label>
          <label class="text-sm">代理 URL（可选）<input v-model="upload.proxy_url" placeholder="socks5h://user:pass@host:port" class="input mt-1 w-full" autocomplete="off" /></label>
          <label class="text-sm">并发上限<input v-model.number="upload.concurrency" type="number" min="1" max="1000" class="input mt-1 w-full" /></label>
          <label class="text-sm md:col-span-2">凭证 JSON（仅写入后端，不会展示）<textarea v-model="upload.credentials" required rows="4" class="input mt-1 w-full font-mono" /></label>
          <label class="text-sm">并发倍率<input v-model.number="upload.concurrency_multiplier" type="number" min="0.1" max="5" step="0.1" class="input mt-1 w-full" /></label>
          <label class="text-sm">售价倍率<input v-model.number="upload.sell_rate" type="number" min="0" max="100" step="0.01" class="input mt-1 w-full" /></label>
          <p v-if="uploadError" class="text-sm text-red-500 md:col-span-2">{{ uploadError }}</p><button class="btn btn-primary md:col-span-2" :disabled="uploading">{{ uploading ? '上传中…' : '立即上线' }}</button>
        </form>
      </section>
      <section class="card p-6" aria-label="共享收益">
        <div class="flex flex-wrap items-center justify-between gap-4">
          <h2 class="font-semibold text-gray-900 dark:text-white">我的共享收益</h2>
          <button class="btn btn-primary" :disabled="transferring || !wallet || Number(wallet.available) <= 0" @click="transfer">
            {{ transferring ? '转入中…' : '全部转入平台余额' }}
          </button>
        </div>
        <div v-if="wallet" class="mt-4 grid grid-cols-2 gap-4 lg:grid-cols-4">
          <div><p class="text-xs text-gray-500">可提取</p><p class="mt-1 text-2xl font-bold text-emerald-600">{{ wallet.available }}</p></div>
          <div><p class="text-xs text-gray-500">待解冻</p><p class="mt-1 text-xl text-gray-900 dark:text-white">{{ wallet.pending }}</p></div>
          <div><p class="text-xs text-gray-500">累计收益</p><p class="mt-1 text-xl text-gray-900 dark:text-white">{{ wallet.total_earned }}</p></div>
          <div><p class="text-xs text-gray-500">已转入余额</p><p class="mt-1 text-xl text-gray-900 dark:text-white">{{ wallet.total_transferred }}</p></div>
        </div>
        <p v-if="wallet && Number(wallet.frozen) > 0" class="mt-3 text-sm text-amber-600">冻结收益：{{ wallet.frozen }}</p>
        <p v-if="message" role="status" class="mt-3 text-sm text-emerald-600">{{ message }}</p>
        <div v-if="walletError" role="alert" class="mt-3 text-sm text-red-500">{{ walletError }} <button class="underline" @click="loadWallet">刷新钱包</button></div>
      </section>
      <div class="flex gap-2" aria-label="账号范围">
        <button class="btn" :class="mode === 'pool' ? 'btn-primary' : 'btn-secondary'" :aria-pressed="mode === 'pool'" @click="changeMode('pool')">共享池</button>
        <button class="btn" :class="mode === 'mine' ? 'btn-primary' : 'btn-secondary'" :aria-pressed="mode === 'mine'" @click="changeMode('mine')">我的账号</button>
      </div>
      <div v-if="loading" class="py-16 text-center text-gray-500" role="status">加载中…</div>
      <div v-else-if="error" role="alert" class="card p-6 text-red-500">{{ error }} <button class="underline" @click="loadCards">重试</button></div>
      <div v-else-if="!cards.length" class="card p-10 text-center text-gray-500">{{ mode === 'mine' ? '你还没有共享账号' : '暂无可用共享账号' }}</div>
      <div v-else class="grid gap-5 sm:grid-cols-2 xl:grid-cols-3">
        <article v-for="card in cards" :key="card.id" class="card min-w-0 overflow-hidden p-6">
          <div class="flex items-start justify-between gap-3">
            <div class="min-w-0"><h2 class="break-words text-lg font-semibold text-gray-900 dark:text-white">{{ card.display_name }}</h2><p class="mt-1 text-sm text-gray-500">{{ card.platform }}</p></div>
            <span class="shrink-0 rounded-full px-3 py-1 text-xs font-medium" :class="card.status === 'active' ? 'bg-emerald-100 text-emerald-700' : 'bg-gray-100 text-gray-600'">{{ labels[card.status] || card.status }}</span>
          </div>
          <div class="mt-6"><p class="text-xs text-gray-500">累计调用</p><p class="mt-1 break-all text-5xl font-black tracking-tight text-primary-600">{{ card.total_call_count.toLocaleString() }}</p></div>
          <div class="mt-4 flex flex-wrap gap-3 text-xs text-gray-500"><span>并发上限 {{ card.concurrency_limit }}</span><span>并发倍率 {{ card.concurrency_multiplier }}x</span><span>售价倍率 {{ card.sell_rate }}x</span></div>
          <p class="mt-3 text-xs text-gray-400">最近调用：{{ timeText(card.last_called_at) }}</p>
          <div v-if="mode === 'mine'" class="mt-3 flex gap-2"><button class="btn btn-secondary btn-sm" @click="toggle(card)">{{ card.status === 'paused' ? '恢复' : '暂停' }}</button><button class="btn btn-secondary btn-sm text-red-500" @click="remove(card)">删除</button></div>
          <div class="mt-6 border-t border-gray-200 pt-4 dark:border-dark-700">
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
  </AppLayout>
</template>
