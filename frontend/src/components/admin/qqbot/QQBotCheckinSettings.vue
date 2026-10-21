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
        <label class="block space-y-1">{{ t('qqbot.rechargeWindowDays') }}<input v-model.number="form.recharge_window_days" data-testid="recharge-window-days" type="number" min="1" max="3650" step="1" required class="input" /></label>
        <div class="grid gap-4 sm:grid-cols-2">
          <label class="space-y-1">{{ t('qqbot.minimumLifetimeRecharge') }}<input v-model.number="form.min_recharge_total" data-testid="min-recharge-total" type="number" min="0" max="1000000000" step="0.01" required class="input" /></label>
          <label class="space-y-1">{{ t('qqbot.minimumRecentRecharge', { days: form.recharge_window_days }) }}<input v-model.number="form.min_recharge_30d" data-testid="min-recharge-30d" type="number" min="0" max="1000000000" step="0.01" required class="input" /></label>
        </div>
      </div>
      <fieldset class="space-y-3 rounded-lg border p-4 dark:border-dark-700" :disabled="busy">
        <legend class="font-medium">{{ t('qqbot.growth.streakTitle') }}</legend>
        <p class="text-sm text-gray-500">{{ t('qqbot.growth.streakHelp') }}</p>
        <div class="grid gap-4 sm:grid-cols-2">
          <label class="block space-y-1">{{ t('qqbot.growth.streakDays') }}<input v-model.number="form.streak_days" data-testid="streak-days" type="number" min="1" max="3650" step="1" required class="input" /></label>
        </div>
        <QQBotGrowthRewardFields v-model="form.streak_reward" prefix="streak" />
      </fieldset>
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
import type { QQCheckinConfig } from '@/api/qqbot'
import { loadCheckinConfig, saveCheckinSettings } from './checkinSettings'
import QQBotGrowthRewardFields from './QQBotGrowthRewardFields.vue'
import { validGrowthReward } from './growthReward'
const { t } = useI18n()
const form = ref<QQCheckinConfig | null>(null)
const busy = ref(false)
const error = ref('')
const notice = ref('')
async function save() {
  if (!form.value || busy.value) return
  error.value = ''; notice.value = ''
  if (!Number.isFinite(form.value.min_amount) || !Number.isFinite(form.value.max_amount) || form.value.min_amount > form.value.max_amount) { error.value = t('qqbot.invalidRange'); return }
  if (![form.value.min_recharge_total, form.value.min_recharge_30d].every(n => Number.isFinite(n) && n >= 0 && n <= 1_000_000_000 && Number(n.toFixed(2)) === n)) { error.value = t('qqbot.invalidRechargeThreshold'); return }
  if (!Number.isInteger(form.value.recharge_window_days) || form.value.recharge_window_days < 1 || form.value.recharge_window_days > 3650) { error.value = t('qqbot.invalidRechargeWindow'); return }
  if (!Number.isInteger(form.value.streak_days) || form.value.streak_days < 1 || form.value.streak_days > 3650) {
    error.value = t('qqbot.growth.invalidStreakDays')
    return
  }
  if (!validGrowthReward(form.value.streak_reward)) {
    error.value = t('qqbot.growth.invalidReward')
    return
  }
  busy.value = true
  try { form.value = await saveCheckinSettings(form.value); notice.value = t('common.saved') }
  catch (e) { error.value = (e as Error).message || t('common.error') }
  finally { busy.value = false }
}
onMounted(async () => {
  try { form.value = await loadCheckinConfig() }
  catch (e) { error.value = (e as Error).message || t('common.error') }
})
</script>
