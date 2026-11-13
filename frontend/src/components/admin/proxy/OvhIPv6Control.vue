<template>
  <div class="mt-3 rounded-xl border border-gray-200 bg-white px-4 py-3 dark:border-dark-700 dark:bg-dark-800">
    <div class="flex flex-wrap items-center gap-x-4 gap-y-2">
      <div class="flex items-center gap-3">
        <span class="text-sm font-medium text-gray-900 dark:text-white">{{ t('ovhIPv6.title') }}</span>
        <button
          type="button"
          role="switch"
          :aria-label="t('ovhIPv6.title')"
          :aria-checked="status?.enabled ?? false"
          :disabled="toggleDisabled"
          :class="[
            'relative inline-flex h-6 w-11 shrink-0 items-center rounded-full transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-500 focus-visible:ring-offset-2 disabled:cursor-not-allowed disabled:opacity-50',
            status?.enabled ? 'bg-primary-600' : 'bg-gray-300 dark:bg-dark-500'
          ]"
          @click="toggleEnabled"
        >
          <span :class="['inline-block h-4 w-4 rounded-full bg-white transition-transform', status?.enabled ? 'translate-x-6' : 'translate-x-1']" />
        </button>
        <span class="text-xs text-gray-500 dark:text-gray-400" aria-live="polite">{{ stateLabel }}</span>
      </div>
      <div v-if="status?.configured" class="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 text-xs text-gray-500 dark:text-gray-400">
        <span v-if="status.prefix" class="break-all">{{ t('ovhIPv6.prefix') }}: <code>{{ status.prefix }}</code></span>
        <span>{{ t('ovhIPv6.bound', { count: status.bound }) }}</span>
      </div>
      <div class="ml-auto flex items-center gap-3">
        <button type="button" class="btn btn-secondary px-3 py-1.5 text-xs" data-testid="open-ipv6-storage" @click="showStorage = true">
          {{ t('ovhIPv6.storage.title') }}
        </button>
        <button
        type="button"
        class="rounded p-1 text-gray-500 hover:text-primary-600 disabled:opacity-50 dark:text-gray-400 dark:hover:text-primary-400"
        :title="t('ovhIPv6.refresh')"
        :aria-label="t('ovhIPv6.refresh')"
        :disabled="loading || saving"
        @click="loadStatus"
      >
        <Icon name="refresh" size="sm" :class="loading ? 'animate-spin' : ''" />
        </button>
      </div>
    </div>
    <p v-if="status" class="mt-2 text-xs leading-relaxed text-gray-500 dark:text-gray-400">
      {{ t(status.enabled ? 'ovhIPv6.enabledHint' : 'ovhIPv6.disabledHint') }}
      {{ t('ovhIPv6.lifecycleHint') }}
    </p>
    <p v-if="error" class="mt-2 text-xs text-red-600 dark:text-red-400" role="alert">{{ error }}</p>
    <p v-else-if="availabilityMessage" class="mt-2 text-xs text-amber-600 dark:text-amber-400" role="status">{{ availabilityMessage }}</p>
    <OvhIPv6StorageDialog :show="showStorage" @close="showStorage = false" />
  </div>
  <TotpStepUpDialog :controller="stepUp" />
</template>

<script setup lang="ts">
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
import { useStepUp, isStepUpCancelled, StepUpCancelledError } from '@/composables/useStepUp'
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { ovhIPv6API, type OvhIPv6Status } from '@/api/admin/ovhIPv6'
import Icon from '@/components/icons/Icon.vue'
import OvhIPv6StorageDialog from './OvhIPv6StorageDialog.vue'
import { extractApiErrorMessage } from '@/utils/apiError'

const { t } = useI18n()
const emit = defineEmits<{ changed: [status: OvhIPv6Status] }>()
const stepUp = useStepUp()
const status = ref<OvhIPv6Status | null>(null)
const loading = ref(false)
const saving = ref(false)
const error = ref('')
const showStorage = ref(false)
let disposed = false
let controller: AbortController | null = null

const toggleDisabled = computed(() => loading.value || saving.value || !status.value || (
  !status.value.enabled && (!status.value.configured || !status.value.available)
))
const stateLabel = computed(() => {
  if (saving.value) return t('ovhIPv6.saving')
  if (loading.value) return t('ovhIPv6.loading')
  if (!status.value) return '—'
  return t(status.value.enabled ? 'ovhIPv6.enabled' : 'ovhIPv6.disabled')
})
const availabilityMessage = computed(() => {
  if (!status.value) return ''
  if (!status.value.configured) return t('ovhIPv6.notConfigured')
  if (!status.value.available) return t('ovhIPv6.unavailable')
  return ''
})

async function loadStatus() {
  if (loading.value || saving.value || disposed) return
  const currentController = new AbortController()
  controller = currentController
  loading.value = true
  error.value = ''
  try {
    const result = await ovhIPv6API.get(currentController.signal)
    if (!disposed) status.value = result
  } catch (cause) {
    if (!currentController.signal.aborted) error.value = extractApiErrorMessage(cause, t('ovhIPv6.loadFailed'))
  } finally {
    loading.value = false
    controller = null
  }
}

async function toggleEnabled() {
  if (toggleDisabled.value || !status.value) return
  const enabled = !status.value.enabled
  saving.value = true
  error.value = ''
  try {
    const result = await stepUp.run(() => {
      if (disposed) throw new StepUpCancelledError()
      return ovhIPv6API.setEnabled(enabled)
    })
    if (!disposed) {
      status.value = result
      emit('changed', result)
    }
  } catch (cause) {
    if (!disposed && !isStepUpCancelled(cause)) error.value = extractApiErrorMessage(cause, t('ovhIPv6.saveFailed'))
  } finally {
    saving.value = false
  }
}

onMounted(loadStatus)
onUnmounted(() => {
  disposed = true
  stepUp.onCancel()
  controller?.abort()
})
defineExpose({ refresh: loadStatus })
</script>
