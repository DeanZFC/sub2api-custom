<template>
  <div class="space-y-6">
    <p class="text-sm text-gray-500">{{ t('qqbot.checkinHelp') }}</p>
    <p v-if="error" role="alert" class="rounded-lg bg-red-50 p-4 text-red-700 dark:bg-red-950">{{ error }}</p>
    <p v-if="notice" role="status" class="rounded-lg bg-green-50 p-4 text-green-700 dark:bg-green-950">{{ notice }}</p>
    <form v-if="form" class="card space-y-5 p-6" @submit.prevent="save">
      <label class="flex items-center gap-2"><input v-model="form.enabled" type="checkbox" />{{ t('qqbot.enableCheckin') }}</label>
      <div class="grid gap-4 sm:grid-cols-2">
        <label class="space-y-1">{{ t('qqbot.minAmount') }}<input v-model.number="form.min_amount" data-testid="min-amount" type="number" min="0" max="1000" step="0.00000001" required class="input" /></label>
        <label class="space-y-1">{{ t('qqbot.maxAmount') }}<input v-model.number="form.max_amount" data-testid="max-amount" type="number" min="0" max="1000" step="0.00000001" required class="input" /></label>
      </div>
      <div class="space-y-3 rounded-lg border p-4 dark:border-dark-700">
        <h2 class="font-medium">{{ t('qqbot.rechargeEligibility') }}</h2>
        <p class="text-sm text-gray-500">{{ t('qqbot.rechargeEligibilityHelp') }}</p>
        <div class="grid gap-4 sm:grid-cols-2">
          <label class="space-y-1">{{ t('qqbot.minimumLifetimeRecharge') }}<input v-model.number="form.min_recharge_total" data-testid="min-recharge-total" type="number" min="0" max="1000000000" step="0.01" required class="input" /></label>
          <label class="space-y-1">{{ t('qqbot.minimum30dRecharge') }}<input v-model.number="form.min_recharge_30d" data-testid="min-recharge-30d" type="number" min="0" max="1000000000" step="0.01" required class="input" /></label>
        </div>
      </div>
      <p class="rounded-lg bg-gray-50 p-4 text-sm dark:bg-dark-800">{{ t('qqbot.checkinRangeHelp') }}</p>
      <p class="text-sm text-gray-500">{{ t('qqbot.checkinCommand') }}</p>
      <button type="submit" class="btn btn-primary" :disabled="busy">{{ t('common.save') }}</button>
    </form>
    <p v-else-if="!error">{{ t('common.loading') }}</p>
  </div>
</template>
<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { qqbotAPI, type QQCheckinConfig } from '@/api/qqbot'
const { t } = useI18n()
const form = ref<QQCheckinConfig | null>(null)
const busy = ref(false)
const error = ref('')
const notice = ref('')
async function save() {
  if (!form.value) return
  error.value = ''; notice.value = ''
  if (!Number.isFinite(form.value.min_amount) || !Number.isFinite(form.value.max_amount) || form.value.min_amount > form.value.max_amount) { error.value = t('qqbot.invalidRange'); return }
  if (![form.value.min_recharge_total, form.value.min_recharge_30d].every(n => Number.isFinite(n) && n >= 0 && n <= 1_000_000_000 && Number(n.toFixed(2)) === n)) { error.value = t('qqbot.invalidRechargeThreshold'); return }
  busy.value = true
  try { form.value = await qqbotAPI.saveCheckin({ ...form.value }); notice.value = t('common.saved') }
  catch (e) { error.value = (e as Error).message || t('common.error') }
  finally { busy.value = false }
}
onMounted(async () => {
  try { form.value = await qqbotAPI.checkinConfig() }
  catch (e) { error.value = (e as Error).message || t('common.error') }
})
</script>
