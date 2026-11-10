<template>
  <BaseDialog :show="show" :title="t('admin.accounts.openaiIPv6.title')" :close-on-escape="!busy" :show-close-button="!busy" @close="close">
    <div class="space-y-4" :aria-busy="busy">
      <p class="font-medium">{{ account?.name }}</p>
      <p class="text-sm text-gray-600 dark:text-gray-300">{{ t('admin.accounts.openaiIPv6.description') }}</p>
      <p class="text-sm text-amber-600">{{ t('admin.accounts.openaiIPv6.drain') }}</p>
      <p v-if="error" role="alert" class="break-words text-sm text-red-600">{{ error }}</p>
      <p v-if="busy" role="status">{{ t('common.loading') }}</p>
      <template v-if="state">
        <p v-if="!state.configured" class="text-sm text-amber-600">{{ t('admin.accounts.openaiIPv6.notConfigured') }}</p>
        <p v-if="!state.eligible" class="text-sm text-amber-600">{{ t('admin.accounts.openaiIPv6.ineligible') }}</p>
        <template v-if="state.binding">
          <p class="break-all font-mono" data-testid="ipv6-address">{{ state.binding.address || '—' }}</p>
          <p>{{ state.binding.status }}</p>
          <p v-if="state.binding.last_error" class="text-sm text-red-600">{{ state.binding.last_error }}</p>
          <p class="text-xs text-gray-500">{{ t('admin.accounts.openaiIPv6.restore') }}</p>
        </template>
        <p v-else>{{ t('admin.accounts.openaiIPv6.unbound') }}</p>
      </template>
      <div v-if="confirmDetach" class="space-y-2 rounded border border-red-200 p-3">
        <p>{{ t('admin.accounts.openaiIPv6.confirmDetach') }}</p>
        <button class="btn btn-danger" :disabled="busy" data-testid="ipv6-confirm-detach" @click="mutate('detach')">{{ t('common.confirm') }}</button>
        <button class="btn btn-secondary" :disabled="busy" @click="confirmDetach = false">{{ t('common.cancel') }}</button>
      </div>
    </div>
    <template #footer>
      <button class="btn btn-secondary" :disabled="busy" @click="refresh">{{ t('common.refresh') }}</button>
      <button v-if="state?.binding" class="btn btn-danger" :disabled="!canMutate" data-testid="ipv6-detach" @click="confirmDetach = true">{{ t('admin.accounts.openaiIPv6.detach') }}</button>
      <button v-if="state && (!state.binding || ['error', 'provisioning'].includes(state.binding.status))" class="btn btn-primary" :disabled="!canMutate" data-testid="ipv6-bind" @click="mutate('bind')">{{ t(state.binding ? 'admin.accounts.openaiIPv6.retry' : 'admin.accounts.openaiIPv6.bind') }}</button>
    </template>
  </BaseDialog>
  <TotpStepUpDialog :controller="stepUp" />
</template>
<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
import { useStepUp, isStepUpCancelled, StepUpCancelledError } from '@/composables/useStepUp'
import { openaiIPv6API, type IPv6State } from '@/api/admin/openaiIPv6'
import type { Account } from '@/types'
const props = defineProps<{ show: boolean; account: Account | null }>()
const emit = defineEmits<{ close: []; updated: [] }>()
const { t } = useI18n()
const stepUp = useStepUp()
const state = ref<IPv6State | null>(null)
const busy = ref(false)
const error = ref('')
const confirmDetach = ref(false)
let generation = 0
const canMutate = computed(() => !busy.value && state.value?.eligible && state.value.configured && props.account?.status === 'inactive')
function message(cause: unknown) { return (cause as { message?: string })?.message || t('admin.accounts.openaiIPv6.failed') }
async function read(id: number, current: number) { const next = await openaiIPv6API.status(id); if (current === generation) state.value = next }
async function refresh() {
  if (busy.value || !props.show || !props.account) return
  const current = generation; const id = props.account.id
  busy.value = true; error.value = ''
  try { await read(id, current) } catch (cause) { if (current === generation) error.value = message(cause) }
  finally { if (current === generation) busy.value = false }
}
async function mutate(kind: 'bind' | 'detach') {
  if (!canMutate.value || !props.account || (kind === 'detach' && !confirmDetach.value)) return
  const id = props.account.id; const current = generation
  busy.value = true; error.value = ''
  try {
    await stepUp.run(async () => {
      if (current !== generation) throw new StepUpCancelledError()
      if (kind === 'bind') await openaiIPv6API.bind(id)
      else await openaiIPv6API.detach(id)
    })
    if (current !== generation) return
    confirmDetach.value = false
    await read(id, current)
  } catch (cause) {
    if (current !== generation || isStepUpCancelled(cause)) return
    error.value = message(cause)
    try { await read(id, current) } catch { /* Preserve the operation error. */ }
  } finally {
    if (current === generation) { busy.value = false; emit('updated') }
  }
}
function close() { if (!busy.value) emit('close') }
watch([() => props.show, () => props.account?.id], () => {
  generation++; stepUp.onCancel(); busy.value = false; state.value = null; error.value = ''; confirmDetach.value = false
  if (props.show) void refresh()
}, { immediate: true })
onUnmounted(() => { generation++; stepUp.onCancel() })
</script>
