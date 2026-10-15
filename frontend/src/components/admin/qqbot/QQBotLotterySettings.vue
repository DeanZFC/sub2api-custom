<template>
  <div class="space-y-6">
    <p class="text-sm text-gray-500">{{ t('qqbot.lotteryHelp') }}</p>
    <p v-if="error" role="alert" class="rounded-lg bg-red-50 p-4 text-red-700 dark:bg-red-950">{{ error }}</p>
    <p v-if="notice" role="status" class="rounded-lg bg-green-50 p-4 text-green-700 dark:bg-green-950">{{ notice }}</p>
    <form v-if="form" class="card space-y-5 p-6" @submit.prevent="save">
      <div class="flex flex-wrap items-center justify-between gap-3"><h2 class="text-lg font-semibold">{{ t('qqbot.lotteryRules') }}</h2><label class="flex items-center gap-2"><input v-model="form.enabled" type="checkbox" />{{ t('common.enabled') }}</label></div>
      <label class="block space-y-1">{{ t('qqbot.activityName') }}<input v-model.trim="form.title" class="input" maxlength="60" required /></label>
      <div class="grid gap-4 sm:grid-cols-2">
        <label class="space-y-1">{{ t('qqbot.rechargeStep') }}<input v-model.number="form.recharge_step" data-testid="recharge-step" class="input" type="number" min="0.01" max="1000000000" step="0.01" required /></label>
        <label class="space-y-1">{{ t('qqbot.chancesPerStep') }}<input v-model.number="form.chances_per_step" data-testid="chances-per-step" class="input" type="number" min="1" max="1000" step="1" required /></label>
      </div>
      <div class="space-y-2 rounded-lg bg-gray-50 p-4 text-sm dark:bg-dark-800"><p>{{ t('qqbot.historicalRechargeHelp') }}</p><p>{{ t('qqbot.lotteryRuleChangeHelp') }}</p></div>
      <div class="flex flex-wrap items-center justify-between gap-3"><h2 class="font-semibold">{{ t('qqbot.prizePool') }}</h2><span :class="probabilityTotal === 10000 ? 'text-green-600' : 'text-red-600'">{{ t('qqbot.probabilityTotal', { total: (probabilityTotal / 100).toFixed(2) }) }}</span></div>
      <div v-for="(prize, index) in form.prizes" :key="index" class="grid items-end gap-3 rounded-lg border p-4 dark:border-dark-700 sm:grid-cols-2 md:grid-cols-[2fr_1fr_1fr_auto]">
        <label class="space-y-1">{{ t('qqbot.prizeName') }}<input v-model.trim="prize.name" class="input" required maxlength="60" /></label>
        <label class="space-y-1">{{ t('qqbot.prizeAmount') }}<input v-model.number="prize.amount" class="input" type="number" min="0" max="1000" step="0.00000001" required /></label>
        <label class="space-y-1">{{ t('qqbot.probability') }}<input :value="prize.probability_bps / 100" :data-testid="`probability-${index}`" class="input" type="number" min="0.01" max="100" step="0.01" required @input="setProbability(index, $event)" /></label>
        <button type="button" class="btn btn-secondary" :disabled="form.prizes.length <= 1" @click="form.prizes.splice(index, 1)">{{ t('common.delete') }}</button>
      </div>
      <button type="button" class="btn btn-secondary" :disabled="form.prizes.length >= 20" @click="addPrize">{{ t('qqbot.addPrize') }}</button>
      <p class="text-sm text-gray-500">{{ t('qqbot.prizeHelp') }}</p>
      <p class="text-sm text-gray-500">{{ t('qqbot.lotteryCommand') }}</p>
      <button type="submit" class="btn btn-primary" :disabled="saving || probabilityTotal !== 10000">{{ t('common.save') }}</button>
    </form>
    <p v-else-if="!error">{{ t('common.loading') }}</p>
    <section class="card space-y-4 p-6">
      <h2 class="text-lg font-semibold">{{ t('qqbot.chanceLookup') }}</h2>
      <form class="flex flex-wrap gap-3" @submit.prevent="lookup"><label class="min-w-0 flex-1"><span class="sr-only">{{ t('qqbot.userID') }}</span><input v-model="userID" class="input" :placeholder="t('qqbot.userID')" type="number" min="1" step="1" required /></label><button class="btn btn-secondary" :disabled="lookingUp">{{ t('common.search') }}</button></form>
      <p v-if="lookupError" role="alert" class="text-red-600">{{ lookupError }}</p>
      <dl v-if="state" class="grid grid-cols-2 gap-4 text-sm sm:grid-cols-4">
        <div><dt class="text-gray-500">{{ t('qqbot.eligibleRecharge') }}</dt><dd class="mt-1 font-semibold">${{ state.recharge_total.toFixed(2) }}</dd></div>
        <div><dt class="text-gray-500">{{ t('qqbot.earnedChances') }}</dt><dd class="mt-1 font-semibold">{{ state.earned }}</dd></div>
        <div><dt class="text-gray-500">{{ t('qqbot.usedChances') }}</dt><dd class="mt-1 font-semibold">{{ state.used }}</dd></div>
        <div><dt class="text-gray-500">{{ t('qqbot.remainingChances') }}</dt><dd class="mt-1 font-semibold">{{ state.remaining }}</dd></div>
      </dl>
      <p v-if="state" class="text-sm text-gray-500">{{ t('qqbot.nextRecharge', { amount: state.next_recharge.toFixed(2) }) }}</p>
    </section>

  </div>
</template>
<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { qqbotAPI, type QQLotteryConfig, type QQLotteryState } from '@/api/qqbot'
const { t } = useI18n()
const form = ref<QQLotteryConfig | null>(null)
const saving = ref(false)
const error = ref('')
const notice = ref('')
const probabilityTotal = computed(() => form.value?.prizes.reduce((sum, p) => sum + p.probability_bps, 0) ?? 0)
const userID = ref('')
const state = ref<QQLotteryState | null>(null)
const lookingUp = ref(false)
const lookupError = ref('')
function message(e: unknown) { return (e as Error).message || t('common.error') }
function setProbability(index: number, event: Event) {
  if (form.value) form.value.prizes[index]!.probability_bps = Math.round(Number((event.target as HTMLInputElement).value) * 100)
}
function addPrize() { form.value?.prizes.push({ name: '', amount: 0, probability_bps: 100 }) }
async function save() {
  if (!form.value || probabilityTotal.value !== 10000) return
  saving.value = true; error.value = ''; notice.value = ''
  try { form.value = await qqbotAPI.saveLottery({ ...form.value, prizes: form.value.prizes.map(p => ({ ...p })) }); notice.value = t('common.saved'); state.value = null }
  catch (e) { error.value = message(e) }
  finally { saving.value = false }
}
async function lookup() {
  lookingUp.value = true; lookupError.value = ''; state.value = null
  try { state.value = await qqbotAPI.lotteryState(Number(userID.value)) }
  catch (e) { lookupError.value = message(e) }
  finally { lookingUp.value = false }
}
onMounted(async () => {
  try { form.value = await qqbotAPI.lotteryConfig() }
  catch (e) { error.value = message(e) }
})
</script>
