<template>
  <BaseDialog
    :show="show"
    :title="t('admin.accounts.openaiVm.title')"
    width="wide"
    :close-on-escape="!busy"
    :show-close-button="!busy"
    @close="close"
  >
    <div class="space-y-5" :aria-busy="busy">
      <div class="rounded-lg bg-sky-50 p-3 text-sm text-sky-900 dark:bg-sky-900/20 dark:text-sky-200">
        <p class="font-medium">{{ account?.name }}</p>
        <p class="mt-1">{{ t('admin.accounts.openaiVm.description') }}</p>
        <p class="mt-1">{{ t('admin.accounts.openaiVm.engineHint') }}</p>
      </div>

      <p v-if="operation" role="status" class="flex items-center gap-2 text-sm text-gray-600 dark:text-gray-300">
        <Icon name="refresh" size="sm" class="animate-spin" />
        {{ t('admin.accounts.openaiVm.working') }}
      </p>
      <p v-if="error" role="alert" class="whitespace-pre-wrap break-words rounded-lg bg-red-50 p-3 text-sm text-red-700 dark:bg-red-900/20 dark:text-red-300">{{ error }}</p>
      <p v-if="notice" role="status" class="rounded-lg bg-emerald-50 p-3 text-sm text-emerald-700 dark:bg-emerald-900/20 dark:text-emerald-300">{{ notice }}</p>

      <section class="space-y-3 rounded-xl border border-gray-200 p-4 dark:border-dark-600">
        <div class="flex items-center justify-between gap-3">
          <h4 class="font-medium text-gray-900 dark:text-white">{{ t('admin.accounts.openaiVm.bindingTitle') }}</h4>
          <button type="button" :disabled="busy" class="btn btn-secondary btn-sm" data-testid="vm-refresh" @click="refresh">{{ t('common.refresh') }}</button>
        </div>
        <p v-if="bindingError" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ bindingError }}</p>
        <p v-if="state && !eligible" class="text-sm text-amber-600 dark:text-amber-400">{{ t('admin.accounts.openaiVm.ineligible') }}</p>
        <template v-if="binding">
          <dl class="grid grid-cols-[auto_1fr] gap-x-4 gap-y-2 text-sm">
            <dt class="text-gray-500 dark:text-gray-400">{{ t('admin.accounts.openaiVm.slotId') }}</dt>
            <dd class="break-all font-mono text-gray-900 dark:text-white">{{ binding.slot_id || '—' }}</dd>
            <dt class="text-gray-500 dark:text-gray-400">{{ t('common.status') }}</dt>
            <dd :class="binding.status === 'ready' ? 'text-emerald-600 dark:text-emerald-400' : 'text-amber-600 dark:text-amber-400'" data-testid="vm-binding-status">{{ statusLabel }}</dd>
            <dt class="text-gray-500 dark:text-gray-400">{{ t('admin.accounts.openaiVm.updatedAt') }}</dt>
            <dd class="break-all text-gray-900 dark:text-white">{{ binding.updated_at }}</dd>
          </dl>
          <p v-if="binding.last_error" role="alert" class="whitespace-pre-wrap break-words rounded-lg bg-red-50 p-3 text-sm text-red-700 dark:bg-red-900/20 dark:text-red-300">{{ binding.last_error }}</p>
          <p v-if="binding.status !== 'ready'" class="text-xs text-amber-600 dark:text-amber-400">{{ t('admin.accounts.openaiVm.notReady') }}</p>
          <div v-if="eligible" class="flex flex-wrap gap-2">
            <button v-if="canRetry" type="button" :disabled="busy || (!state?.configured && !cleanupRetry)" class="btn btn-primary btn-sm" data-testid="vm-retry" @click="mutate('retry')">{{ t(cleanupRetry ? 'admin.accounts.openaiVm.retryCleanup' : 'admin.accounts.openaiVm.retry') }}</button>
            <button type="button" :disabled="busy" class="btn btn-danger btn-sm" data-testid="vm-delete" @click="confirmDelete = true">{{ t('admin.accounts.openaiVm.unbind') }}</button>
          </div>
        </template>
        <template v-else-if="state">
          <p class="text-sm text-gray-600 dark:text-gray-300">{{ t('admin.accounts.openaiVm.unbound') }}</p>
          <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.accounts.openaiVm.proxyHint') }}</p>
          <p v-if="!state.configured" class="text-sm text-amber-600 dark:text-amber-400">{{ t('admin.accounts.openaiVm.notConfigured') }}</p>
          <div v-if="eligible" class="flex flex-wrap items-end gap-3">
            <div>
              <label for="openai-vm-runtime" class="input-label">{{ t('admin.accounts.openaiVm.runtime') }}</label>
              <select id="openai-vm-runtime" v-model="runtime" class="input" :disabled="busy">
                <option value="docker">Docker</option>
                <option value="kvm">KVM</option>
              </select>
            </div>
            <button type="button" :disabled="busy || !state.configured" class="btn btn-primary" data-testid="vm-create" @click="mutate('create')">{{ t('admin.accounts.openaiVm.create') }}</button>
          </div>
        </template>
        <RouterLink v-if="state && !state.configured" v-slot="{ href, navigate }" to="/admin/settings?tab=openai-vm" custom>
          <a :href="busy ? undefined : href" :aria-disabled="busy" :tabindex="busy ? -1 : undefined" class="inline-block text-sm text-primary-600 hover:underline aria-disabled:cursor-not-allowed aria-disabled:opacity-50" data-testid="vm-settings-link" @click="openSettings($event, navigate)">{{ t('admin.accounts.openaiVm.configureGlobally') }}</a>
        </RouterLink>
        <div v-if="confirmDelete" class="space-y-3 rounded-lg border border-red-200 bg-red-50 p-3 dark:border-red-900 dark:bg-red-900/20">
          <p class="text-sm text-red-700 dark:text-red-300">{{ t('admin.accounts.openaiVm.unbindConfirm') }}</p>
          <div class="flex gap-2">
            <button type="button" :disabled="busy" class="btn btn-danger btn-sm" data-testid="vm-delete-confirm" @click="mutate('delete')">{{ t('admin.accounts.openaiVm.unbind') }}</button>
            <button type="button" :disabled="busy" class="btn btn-secondary btn-sm" @click="confirmDelete = false">{{ t('common.cancel') }}</button>
          </div>
        </div>
      </section>
    </div>
    <template #footer>
      <div class="flex justify-end">
        <button type="button" class="btn btn-secondary" :disabled="busy" @click="close">{{ t('common.close') }}</button>
      </div>
    </template>
  </BaseDialog>
  <TotpStepUpDialog :controller="stepUp" />
</template>

<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink, type NavigationFailure } from 'vue-router'
import BaseDialog from '@/components/common/BaseDialog.vue'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
import { Icon } from '@/components/icons'
import { isStepUpBlocked, isStepUpCancelled, StepUpCancelledError, stepUpBlockReason, useStepUp } from '@/composables/useStepUp'
import { openaiVmAPI, type OpenAIVMAccountState, type OpenAIVMBinding, type OpenAIVMRuntime } from '@/api/admin/openaiVm'
import type { Account } from '@/types'

const props = defineProps<{ show: boolean; account: Account | null }>()
const emit = defineEmits<{ (e: 'close'): void; (e: 'updated'): void }>()
const { t } = useI18n()
const stepUp = useStepUp()
const state = ref<OpenAIVMAccountState | null>(null)
const runtime = ref<OpenAIVMRuntime>('docker')
const operation = ref('')
const error = ref('')
const notice = ref('')
const bindingError = ref('')
const confirmDelete = ref(false)
let session = 0

const busy = computed(() => operation.value !== '')
const binding = computed(() => state.value?.binding ?? null)
const eligible = computed(() => Boolean(state.value?.eligible && props.account?.platform === 'openai' && props.account.type === 'oauth' && props.account.parent_account_id == null))
const cleanupRetry = computed(() => ['deleting', 'delete_failed'].includes(binding.value?.status ?? ''))
const canRetry = computed(() => ['provisioning', 'error'].includes(binding.value?.status ?? '') || cleanupRetry.value)
const statusLabel = computed(() => {
  const status = binding.value?.status ?? ''
  return ['provisioning', 'ready', 'error', 'deleting', 'delete_failed'].includes(status)
    ? t(`admin.accounts.openaiVm.status.${status}`)
    : status
})

function messageOf(cause: unknown): string {
  if (isStepUpBlocked(cause)) {
    return t(stepUpBlockReason(cause) === 'STEP_UP_ADMIN_API_KEY_FORBIDDEN' ? 'stepUp.adminApiKeyForbidden' : 'stepUp.notEnabled')
  }
  const message = (cause as { message?: unknown } | null)?.message
  return typeof message === 'string' && message ? message : t('admin.accounts.openaiVm.failed')
}

async function readBinding(accountId: number, current: number) {
  const value = await openaiVmAPI.getBinding(accountId)
  if (current !== session) return
  state.value = value
  bindingError.value = ''
}

async function load() {
  if (busy.value || !props.show || !props.account) return
  const current = session
  const accountId = props.account.id
  operation.value = 'load'
  try {
    await readBinding(accountId, current)
  } catch (cause) {
    if (current === session) bindingError.value = messageOf(cause)
  } finally {
    if (current === session) operation.value = ''
  }
}

async function run(kind: string, action: (accountId: number, current: number) => Promise<void>) {
  if (busy.value || !props.account || !props.show) return
  const current = session
  const accountId = props.account.id
  operation.value = kind
  notice.value = ''
  try {
    await action(accountId, current)
    if (current === session) error.value = ''
  } catch (cause) {
    if (current !== session || isStepUpCancelled(cause)) return
    error.value = messageOf(cause)
    // A timeout can still leave a server-side binding. Refresh without hiding the error.
    if (['create', 'retry', 'delete'].includes(kind)) {
      try { await readBinding(accountId, current) } catch (refreshError) {
        if (current === session) bindingError.value = messageOf(refreshError)
      }
      if (current === session) emit('updated')
    }
  } finally {
    if (current === session) operation.value = ''
  }
}

async function refresh() {
  await run('refresh', readBinding)
}

async function mutate(kind: 'create' | 'retry' | 'delete') {
  if (!eligible.value) return
  if (kind === 'create' && (binding.value || !state.value?.configured)) return
  if (kind === 'retry' && (!canRetry.value || (!state.value?.configured && !cleanupRetry.value))) return
  if (kind === 'delete' && (!binding.value || !confirmDelete.value)) return
  const runtimeType = runtime.value
  const cleaningUp = kind === 'delete' || (kind === 'retry' && cleanupRetry.value)
  await run(kind, async (accountId, current) => {
    const result = await stepUp.run<OpenAIVMBinding | null | void>(async () => {
      if (current !== session) throw new StepUpCancelledError()
      try {
        return await (kind === 'create'
          ? openaiVmAPI.createBinding(accountId, runtimeType)
          : kind === 'retry'
            ? openaiVmAPI.retryBinding(accountId)
            : openaiVmAPI.deleteBinding(accountId))
      } catch (cause) {
        if (current !== session) throw new StepUpCancelledError()
        throw cause
      }
    })
    if (current !== session) return
    if (state.value) state.value = { ...state.value, binding: result ?? null }
    confirmDelete.value = false
    await readBinding(accountId, current)
    if (current !== session) return
    if (cleaningUp) {
      if (binding.value) throw new Error(t('admin.accounts.openaiVm.cleanupUnconfirmed'))
      notice.value = t('admin.accounts.openaiVm.unboundSuccess')
    } else {
      if (!binding.value) throw new Error(t('admin.accounts.openaiVm.createUnconfirmed'))
      if (binding.value.status === 'ready') notice.value = t('admin.accounts.openaiVm.readySuccess')
    }
    emit('updated')
  })
}

function close() {
  if (!busy.value) emit('close')
}

async function openSettings(event: MouseEvent, navigate: (event?: MouseEvent) => Promise<void | NavigationFailure>) {
  if (busy.value) {
    event.preventDefault()
    return
  }
  if (event.metaKey || event.altKey || event.ctrlKey || event.shiftKey || event.button !== 0) return
  await run('navigate', async (_accountId, current) => {
    const failure = await navigate(event)
    if (!failure && current === session) emit('close')
  })
}

watch([() => props.show, () => props.account?.id], () => {
  session += 1
  stepUp.onCancel()
  operation.value = ''
  state.value = null
  error.value = ''
  notice.value = ''
  bindingError.value = ''
  confirmDelete.value = false
  runtime.value = 'docker'
  if (props.show && props.account) void load()
}, { immediate: true })

onUnmounted(() => { session += 1; stepUp.onCancel() })
</script>
