<template>
  <AppLayout>
    <div class="mx-auto max-w-3xl space-y-6 pb-8">
      <div v-if="loading" class="card p-10 text-center text-gray-500" role="status">{{ t('gptDirect.loading') }}</div>
      <div v-else-if="!config" class="card space-y-4 p-8 text-center">
        <p role="alert">{{ t('gptDirect.loadFailed') }}</p>
        <button class="btn btn-secondary" @click="load">{{ t('gptDirect.retry') }}</button>
      </div>
      <div v-else-if="!config.enabled" class="card p-10 text-center text-gray-500">{{ t('gptDirect.disabled') }}</div>
      <template v-else>
        <section class="card overflow-hidden">
          <div class="flex flex-col gap-5 bg-gradient-to-br from-primary-50 via-white to-white p-6 dark:from-primary-950/30 dark:via-dark-800 dark:to-dark-800 sm:flex-row sm:items-center sm:justify-between sm:p-8">
            <div>
              <span class="text-xs font-semibold uppercase tracking-widest text-primary-600 dark:text-primary-400">ChatGPT</span>
              <h2 class="mt-2 text-xl font-semibold text-gray-900 dark:text-white">{{ t('gptDirect.purchaseTitle') }}</h2>
              <p class="mt-2 max-w-lg whitespace-pre-wrap text-sm leading-6 text-gray-500 dark:text-gray-400">{{ config.description || t('gptDirect.purchaseHint') }}</p>
            </div>
            <a v-if="safeURL(config.purchase_url)" :href="safeURL(config.purchase_url)" target="_blank" rel="noopener noreferrer" class="btn btn-primary shrink-0">
              {{ config.button_text || t('gptDirect.buy') }} <Icon name="externalLink" size="sm" class="ml-2" />
            </a>
          </div>
          <p class="border-t border-gray-100 px-6 py-3 text-xs text-gray-500 dark:border-dark-700 sm:px-8">{{ t('gptDirect.balanceHint') }}</p>
        </section>

        <section class="card p-6 sm:p-8">
          <div class="mb-6 flex items-center justify-between gap-3">
            <h2 class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('gptDirect.redeemTitle') }}</h2>
            <button class="text-sm text-primary-600 hover:underline" @click="showGuide = !showGuide">{{ t('gptDirect.guide') }}</button>
          </div>
          <div v-if="showGuide" class="mb-6 space-y-2 rounded-xl bg-gray-50 p-4 text-sm leading-6 dark:bg-dark-700">
            <p>{{ t('gptDirect.guideText') }}</p>
            <a href="https://chatgpt.com/api/auth/session" target="_blank" rel="noopener noreferrer" class="text-primary-600 hover:underline">{{ t('gptDirect.openSession') }} ↗</a>
            <p class="text-xs text-gray-500">{{ t('gptDirect.sessionPrivacy') }}</p>
          </div>
          <p v-if="pending" class="mb-5 rounded-xl bg-amber-50 p-4 text-sm text-amber-800 dark:bg-amber-950/20 dark:text-amber-300">{{ t('gptDirect.pendingHint') }}</p>
          <p v-if="error" role="alert" class="mb-5 rounded-xl bg-red-50 p-4 text-sm text-red-700 dark:bg-red-950/20 dark:text-red-300">{{ error }}</p>

          <div v-if="receipt || restoring" class="space-y-5" aria-live="polite">
            <template v-if="receipt">
              <div class="rounded-xl bg-gray-50 p-5 dark:bg-dark-700">
                <p class="font-semibold" :class="receipt.task.status === 'success' ? 'text-emerald-600' : ''">{{ statusText(receipt.task.status, receipt.task.fulfillmentKind || receipt.task.planKind) }}</p>
                <p v-if="!terminal" class="mt-2 text-sm text-gray-500">{{ t('gptDirect.processingHint') }}</p>
                <dl class="mt-4 grid grid-cols-[auto_1fr] gap-x-5 gap-y-3 text-sm">
                  <dt class="text-gray-500">{{ t('gptDirect.plan') }}</dt><dd>{{ receipt.task.planName || receipt.task.plan }}</dd>
                  <dt class="text-gray-500">{{ t('gptDirect.email') }}</dt><dd class="break-all">{{ receipt.task.email || '—' }}</dd>
                  <dt class="text-gray-500">{{ t('gptDirect.taskID') }}</dt><dd class="break-all font-mono text-xs">{{ receipt.task.id }}</dd>
                </dl>
              </div>
              <div class="flex flex-wrap gap-3">
                <button class="btn btn-secondary" :disabled="refreshing" @click="refreshTask(false)">{{ refreshing ? t('gptDirect.working') : t('gptDirect.refresh') }}</button>
                <button v-if="terminal" class="btn btn-primary" @click="reset">{{ t('gptDirect.another') }}</button>
              </div>
            </template>
            <template v-else>
              <p class="text-sm text-gray-500">{{ t('gptDirect.restoring') }}</p>
              <button class="btn btn-secondary" :disabled="refreshing" @click="refreshTask(false)">{{ t('gptDirect.retry') }}</button>
              <button class="btn btn-secondary ml-3" :disabled="refreshing" @click="reset">{{ t('gptDirect.back') }}</button>
            </template>
          </div>

          <template v-else>
            <ol class="mb-7 grid grid-cols-3 gap-2" :aria-label="t('gptDirect.stepsLabel')">
              <li v-for="(label, index) in [t('gptDirect.stepCode'), t('gptDirect.stepAccount'), t('gptDirect.stepConfirm')]" :key="label" :aria-current="step === index ? 'step' : undefined" class="border-t-2 pt-3 text-xs sm:text-sm" :class="step >= index ? 'border-primary-500 text-primary-600 dark:text-primary-400' : 'border-gray-200 text-gray-400 dark:border-dark-600'">{{ index + 1 }}. {{ label }}</li>
            </ol>
            <form v-if="step === 0" class="space-y-4" @submit.prevent="verify">
              <label class="block" for="gpt-code"><span class="input-label">{{ t('gptDirect.code') }}</span></label>
              <input id="gpt-code" v-model="code" class="input font-mono" :placeholder="t('gptDirect.codePlaceholder')" autocomplete="off" :spellcheck="false" maxlength="80" required :disabled="busy" />
              <button type="submit" class="btn btn-primary w-full" :disabled="busy || !code.trim()">{{ busy ? t('gptDirect.working') : t('gptDirect.verify') }}</button>
            </form>
            <form v-else-if="step === 1" class="space-y-4" @submit.prevent="checkAccount">
              <div class="rounded-lg bg-primary-50 p-3 text-sm text-primary-700 dark:bg-primary-950/30 dark:text-primary-300">{{ plan?.planName || plan?.plan }}</div>
              <label class="block" for="gpt-session"><span class="input-label">{{ t('gptDirect.session') }}</span></label>
              <textarea id="gpt-session" v-model="session" class="input font-mono text-xs" rows="7" autocomplete="off" :spellcheck="false" maxlength="65536" required :disabled="busy" :placeholder="t('gptDirect.sessionPlaceholder')" />
              <p class="text-xs leading-5 text-gray-500">{{ t('gptDirect.sessionPrivacy') }}</p>
              <div class="flex justify-between gap-3">
                <button type="button" class="btn btn-secondary" :disabled="busy" @click="step = 0; session = ''; error = ''">{{ t('gptDirect.back') }}</button>
                <button type="submit" class="btn btn-primary" :disabled="busy || !session.trim()">{{ busy ? t('gptDirect.working') : t('gptDirect.checkAccount') }}</button>
              </div>
            </form>
            <div v-else class="space-y-5">
              <dl class="grid grid-cols-[auto_1fr] gap-x-5 gap-y-4 rounded-xl bg-gray-50 p-5 text-sm dark:bg-dark-700">
                <dt class="text-gray-500">{{ t('gptDirect.plan') }}</dt><dd>{{ plan?.planName || plan?.plan }}</dd>
                <dt class="text-gray-500">{{ subscription ? t('gptDirect.email') : t('gptDirect.unverifiedEmail') }}</dt><dd class="break-all">{{ subscription?.email || localEmail || t('gptDirect.unknownAccount') }}</dd>
                <template v-if="subscription">
                  <dt class="text-gray-500">{{ t('gptDirect.currentPlan') }}</dt><dd>{{ subscription.plan || subscription.planKey || '—' }}</dd>
                  <dt class="text-gray-500">{{ t('gptDirect.expires') }}</dt><dd>{{ subscription.expiresAt ? formatDate(subscription.expiresAt) : '—' }}</dd>
                </template>
              </dl>
              <p v-if="!subscription" class="text-sm text-amber-700 dark:text-amber-300">{{ t('gptDirect.subscriptionUnavailable') }}</p>
              <label class="flex items-start gap-3 text-sm leading-6">
                <input v-model="accepted" data-testid="gpt-confirm" type="checkbox" class="mt-1 h-4 w-4" :disabled="busy" />
                <span>{{ subscription ? t('gptDirect.confirmHint') : t('gptDirect.confirmUnverified') }}</span>
              </label>
              <div class="flex justify-between gap-3">
                <button class="btn btn-secondary" :disabled="busy" @click="step = 1; accepted = false; error = ''">{{ t('gptDirect.back') }}</button>
                <button data-testid="gpt-submit" class="btn btn-primary" :disabled="busy || !accepted" @click="redeem">{{ busy ? t('gptDirect.submitting') : t('gptDirect.confirm') }}</button>
              </div>
            </div>
          </template>
        </section>

        <section class="card p-6 sm:p-8">
          <h2 class="text-base font-semibold">{{ t('gptDirect.queryTitle') }}</h2>
          <p class="mt-1 text-sm text-gray-500">{{ t('gptDirect.queryHint') }}</p>
          <form class="mt-4 flex flex-col gap-3 sm:flex-row" @submit.prevent="query">
            <input v-model="queryCode" aria-label="CDK" class="input min-w-0 flex-1 font-mono" :placeholder="t('gptDirect.codePlaceholder')" autocomplete="off" :spellcheck="false" maxlength="80" required :disabled="querying || busy" />
            <button class="btn btn-secondary shrink-0" :disabled="querying || busy || !queryCode.trim()">{{ querying ? t('gptDirect.working') : t('gptDirect.query') }}</button>
          </form>
          <p v-if="queryError" role="alert" class="mt-4 text-sm text-red-600">{{ queryError }}</p>
          <div v-if="queryResult" class="mt-4 space-y-2 rounded-lg bg-gray-50 p-4 text-sm dark:bg-dark-700" role="status">
            <p>{{ queryResult.planName || queryResult.plan }} · {{ codeStatusText(queryResult.status) }}</p>
            <p v-if="queryResult.taskStatus">{{ statusText(queryResult.taskStatus, queryResult.planKind) }}</p>
            <p v-if="queryResult.email" class="break-all">{{ queryResult.email }}</p>
          </div>
        </section>
        <div v-if="safeURL(config.support_url)" class="text-center">
          <a :href="safeURL(config.support_url)" target="_blank" rel="noopener noreferrer" class="text-sm text-gray-500 hover:text-primary-600">{{ t('gptDirect.support') }} ↗</a>
        </div>
      </template>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import { useAuthStore } from '@/stores/auth'
import { gptDirectAPI, type GPTDirectCode, type GPTDirectConfig, type GPTDirectReceipt, type GPTDirectSubscription } from '@/api/gptDirect'

const { t, te } = useI18n()
const auth = useAuthStore()
const loading = ref(true)
const config = ref<GPTDirectConfig | null>(null)
const busy = ref(false)
const refreshing = ref(false)
const querying = ref(false)
const error = ref('')
const queryError = ref('')
const showGuide = ref(false)
const step = ref(0)
const code = ref('')
const session = ref('')
const localEmail = ref('')
const plan = ref<GPTDirectCode | null>(null)
const subscription = ref<GPTDirectSubscription | null>(null)
const accepted = ref(false)
const receipt = ref<GPTDirectReceipt | null>(null)
const restoring = ref<{ id: string; token: string } | null>(null)
const pending = ref<{ key: string; codeHash: string } | null>(null)
const queryCode = ref('')
const queryResult = ref<GPTDirectCode | null>(null)
const terminal = computed(() => !!receipt.value && ['success', 'failed', 'cancelled'].includes(receipt.value.task.status))
const storageKey = computed(() => `gpt-direct:${auth.user?.id}:${config.value?.receipt_scope}`)
let timer: ReturnType<typeof setTimeout> | undefined
let disposed = false
let failures = 0

function safeURL(value: string | undefined): string | undefined {
  try { const url = new URL(value || ''); return ['https:', 'http:'].includes(url.protocol) && !url.username && !url.password ? url.href : undefined }
  catch { return undefined }
}
function formatDate(value: string) {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? '—' : date.toLocaleString()
}
function statusText(status: string, kind?: string) {
  if (status === 'success' && ['credits', 'renewal_binding'].includes(kind || '')) return t(`gptDirect.status.${kind}`)
  return te(`gptDirect.status.${status}`) ? t(`gptDirect.status.${status}`) : t('gptDirect.status.queued')
}
function codeStatusText(status: string) {
  return te(`gptDirect.codeStatus.${status}`) ? t(`gptDirect.codeStatus.${status}`) : t('gptDirect.codeStatus.unavailable')
}
function errorText(value: unknown) {
  const failure = value as { reason?: string; status?: number; metadata?: { retry_after?: string } }
  const reason = failure?.status === 429 ? 'RATE_LIMIT' : failure?.reason || 'UPSTREAM_UNAVAILABLE'
  return te(`gptDirect.errors.${reason}`) ? t(`gptDirect.errors.${reason}`) : t('gptDirect.errors.UPSTREAM_UNAVAILABLE')
}
function storeState() {
  if (disposed) return
  try {
    // Never persist the CDK, Session, or account details, even in sessionStorage.
    const task = receipt.value ? { id: receipt.value.task.id, token: receipt.value.token } : restoring.value
    if (task || pending.value) sessionStorage.setItem(storageKey.value, JSON.stringify({ task, pending: pending.value }))
    else sessionStorage.removeItem(storageKey.value)
  } catch { /* Storage may be unavailable in private browsing. */ }
}
async function load() {
  loading.value = true
  try {
    config.value = await gptDirectAPI.config()
    if (!config.value.enabled || disposed) return
    try {
      const stored = JSON.parse(sessionStorage.getItem(storageKey.value) || 'null')
      if (typeof stored?.task?.id === 'string' && /^[A-Za-z0-9_-]{1,128}$/.test(stored.task.id) && /^[A-Za-z0-9_-]{43}$/.test(stored.task.token)) restoring.value = { id: stored.task.id, token: stored.task.token }
      if (typeof stored?.pending?.key === 'string' && /^[A-Za-z0-9_.:-]{8,160}$/.test(stored.pending.key) && /^[a-f0-9]{64}$/.test(stored.pending.codeHash)) pending.value = { key: stored.pending.key, codeHash: stored.pending.codeHash }
    } catch { /* Invalid or legacy storage is ignored. */ }
    if (restoring.value) void refreshTask(false)
  } catch { config.value = null }
  finally { loading.value = false }
}
async function verify() {
  if (busy.value) return
  busy.value = true
  error.value = ''
  try {
    plan.value = await gptDirectAPI.verify(code.value.trim())
    subscription.value = null
    accepted.value = false
    step.value = 1
  } catch (e) { error.value = errorText(e) }
  finally { busy.value = false }
}
async function checkAccount() {
  if (busy.value) return
  busy.value = true
  error.value = ''
  subscription.value = null
  localEmail.value = ''
  accepted.value = false
  try {
    const value = session.value.trim()
    if (value.startsWith('{')) {
      let parsed: Record<string, unknown>
      try { parsed = JSON.parse(value) } catch { error.value = t('gptDirect.errors.INVALID_SESSION'); return }
      if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) { error.value = t('gptDirect.errors.INVALID_SESSION'); return }
      const user = parsed.user as { email?: unknown } | undefined
      localEmail.value = typeof user?.email === 'string' ? user.email : typeof parsed.email === 'string' ? parsed.email : ''
    }
    try { subscription.value = await gptDirectAPI.subscription(value) }
    catch (e) {
      const failure = e as { reason?: string; status?: number }
      if (failure.status === 429 || ['INVALID_SESSION', 'SESSION_EXPIRED', 'INVALID_ACCESS_TOKEN', 'SESSION_REQUIRED', 'DISABLED'].includes(failure.reason || '')) { error.value = errorText(e); return }
      // CTAI also accepts session-token-only redemptions when subscription lookup
      // is unavailable. Require explicit acknowledgement on the next screen.
    }
    step.value = 2
  } finally { busy.value = false }
}
async function redeem() {
  if (busy.value || !accepted.value || step.value !== 2) return
  busy.value = true
  error.value = ''
  try {
    const bytes = new TextEncoder().encode(code.value.trim().toUpperCase())
    const codeHash = Array.from(new Uint8Array(await crypto.subtle.digest('SHA-256', bytes)), byte => byte.toString(16).padStart(2, '0')).join('')
    if (pending.value?.codeHash !== codeHash) pending.value = { key: crypto.randomUUID(), codeHash }
    storeState()
    const result = await gptDirectAPI.redeem(code.value.trim(), session.value, pending.value!.key)
    if (disposed) return
    receipt.value = result
    pending.value = null
    session.value = ''
    code.value = ''
    localEmail.value = ''
    subscription.value = null
    storeState()
    failures = 0
    schedulePoll()
  } catch (e) { if (!disposed) error.value = errorText(e) }
  finally { busy.value = false }
}
function schedulePoll(delay = 10000) {
  clearTimeout(timer)
  if (disposed || terminal.value || (!receipt.value && !restoring.value)) return
  timer = setTimeout(() => {
    if (document.hidden) schedulePoll(15000)
    else void refreshTask(true)
  }, delay)
}
async function refreshTask(automatic: boolean) {
  if (refreshing.value || disposed) return
  const target = receipt.value ? { id: receipt.value.task.id, token: receipt.value.token } : restoring.value
  if (!target) return
  clearTimeout(timer)
  refreshing.value = true
  if (!automatic) error.value = ''
  let retryDelay = 10000
  let retry = true
  try {
    const task = await gptDirectAPI.task(target.id, target.token)
    if (disposed) return
    receipt.value = { task, token: target.token }
    restoring.value = null
    error.value = ''
    failures = 0
    storeState()
  } catch (e) {
    if (disposed) return
    error.value = errorText(e)
    failures++
    const failure = e as { status?: number; metadata?: { retry_after?: string } }
    retry = failure.status !== 403 && failure.status !== 404
    retryDelay = Math.max(Math.min(300000, 10000 * 2 ** Math.min(failures, 5)), Math.min(3600000, Number(failure.metadata?.retry_after || 0) * 1000))
  } finally {
    refreshing.value = false
    if (retry) schedulePoll(retryDelay)
  }
}
async function query() {
  if (querying.value || busy.value) return
  querying.value = true
  queryError.value = ''
  queryResult.value = null
  try { queryResult.value = await gptDirectAPI.query(queryCode.value.trim()) }
  catch (e) { queryError.value = errorText(e) }
  finally { querying.value = false }
}
function reset() {
  clearTimeout(timer)
  receipt.value = null
  restoring.value = null
  step.value = 0
  session.value = ''
  code.value = ''
  plan.value = null
  subscription.value = null
  localEmail.value = ''
  accepted.value = false
  error.value = ''
  storeState()
}
onMounted(load)
onBeforeUnmount(() => { disposed = true; clearTimeout(timer); session.value = ''; code.value = ''; queryCode.value = '' })
</script>
