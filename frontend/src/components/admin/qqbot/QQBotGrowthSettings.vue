<template>
  <div class="space-y-6">
    <p class="text-sm text-gray-500">{{ t('qqbot.growth.help') }}</p>
    <p v-if="error" role="alert" class="rounded-lg bg-red-50 p-4 text-red-700 dark:bg-red-950">{{ error }}</p>
    <p v-if="notice" role="status" class="rounded-lg bg-green-50 p-4 text-green-700 dark:bg-green-950">{{ notice }}</p>
    <form v-if="form" data-testid="one-time-growth-form" class="card space-y-5 p-6" @submit.prevent="save">
      <fieldset class="min-w-0 space-y-4" :disabled="busy">
        <legend class="mb-2 font-medium">{{ t('qqbot.growth.tasksTitle') }}</legend>
        <p class="text-sm text-gray-500">{{ t('qqbot.growth.taskSummary', { total: form.growth.tasks.length, enabled: enabledTasks }) }}</p>
        <p class="rounded-lg bg-gray-50 p-3 text-sm text-gray-600 dark:bg-dark-800 dark:text-gray-300">{{ t('qqbot.growth.identityHelp') }}</p>
        <article v-for="(task, index) in form.growth.tasks" :key="task.id" data-testid="growth-task" class="space-y-4 rounded-lg border p-4 dark:border-dark-700">
          <div class="flex flex-wrap items-center justify-between gap-3">
            <h3 class="font-medium">{{ t('qqbot.growth.taskNumber', { number: index + 1 }) }}</h3>
            <div class="flex items-center gap-4">
              <label class="flex items-center gap-2 text-sm"><input v-model="task.enabled" :data-testid="'task-enabled-' + index" type="checkbox" />{{ t('qqbot.growth.enabled') }}</label>
              <button type="button" class="text-sm text-red-600 hover:text-red-700 dark:text-red-400" :data-testid="'task-remove-' + index" :aria-label="t('qqbot.growth.removeTask', { number: index + 1 })" @click="form.growth.tasks.splice(index, 1)">{{ t('common.delete') }}</button>
            </div>
          </div>
          <div class="grid gap-4 sm:grid-cols-2">
            <label class="block space-y-1">{{ t('qqbot.growth.name') }}<input v-model="task.name" :data-testid="'task-name-' + index" maxlength="40" required class="input" /></label>
            <label class="block space-y-1">{{ t('qqbot.growth.kind') }}
              <select v-model="task.kind" :data-testid="'task-kind-' + index" class="input" @change="task.target = targets[task.kind]">
                <option v-for="kind in kinds" :key="kind" :value="kind">{{ t('qqbot.growth.kinds.' + kind) }}</option>
              </select>
            </label>
            <label class="block space-y-1">{{ t('qqbot.growth.targets.' + task.kind) }}<input v-model.number="task.target" :data-testid="'task-target-' + index" type="number" :min="isAmountKind(task.kind) ? 0.01 : 1" :max="targetLimit(task.kind)" :step="isAmountKind(task.kind) ? 0.01 : 1" :readonly="task.kind === 'binding'" required class="input" /></label>
          </div>
          <QQBotGrowthRewardFields v-model="task.reward" prefix="task" :index="index" />
          <p class="text-sm text-gray-500">{{ t('qqbot.growth.typeHelp.' + task.kind) }}</p>
          <p class="text-sm text-gray-500">{{ t('qqbot.growth.claimCommand', { number: index + 1 }) }}</p>
        </article>
        <p v-if="!form.growth.tasks.length" class="rounded-lg border border-dashed p-6 text-center text-sm text-gray-500 dark:border-dark-700">{{ t('qqbot.growth.empty') }}</p>
        <div class="flex flex-wrap items-end gap-3">
          <label class="min-w-0 flex-1 space-y-1">{{ t('qqbot.growth.preset') }}
            <select v-model="newTaskKind" data-testid="task-preset" class="input">
              <option v-for="kind in kinds" :key="kind" :value="kind">{{ t('qqbot.growth.kinds.' + kind) }}</option>
            </select>
          </label>
          <button type="button" class="btn btn-secondary" data-testid="add-growth-task" :disabled="form.growth.tasks.length >= maxTasks" @click="addTask">{{ t('qqbot.growth.addTask') }}</button>
        </div>
        <p class="text-sm text-gray-500">{{ t('qqbot.growth.limitHelp', { max: maxTasks }) }}</p>
        <p class="text-sm text-gray-500">{{ t('qqbot.growth.commands') }}</p>
      </fieldset>
      <button type="submit" class="btn btn-primary" :disabled="busy">{{ t('common.save') }}</button>
    </form>
    <p v-else-if="!error">{{ t('common.loading') }}</p>
    <section class="space-y-4" data-testid="periodic-growth-tasks">
      <h2 class="text-lg font-semibold">{{ t('qqbot.growth.periodicTitle') }}</h2>
      <p class="text-sm text-gray-500">{{ t('qqbot.growth.periodicHelp') }}</p>
      <QQBotRankingSettings />
    </section>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import type { QQCheckinConfig, QQGrowthTask, QQGrowthTaskKind } from '@/api/qqbot'
import { loadCheckinConfig, saveGrowthSettings } from './checkinSettings'
import QQBotGrowthRewardFields from './QQBotGrowthRewardFields.vue'
import QQBotRankingSettings from './QQBotRankingSettings.vue'
import { normalizeGrowthReward, rewardEnabled, validGrowthReward } from './growthReward'

const { t } = useI18n()
const kinds: QQGrowthTaskKind[] = ['binding', 'usage_count', 'checkin_total', 'checkin_streak', 'recharge_total', 'usage_tokens', 'usage_amount']
const targets: Record<QQGrowthTaskKind, number> = { binding: 1, usage_count: 10, checkin_total: 7, checkin_streak: 7, recharge_total: 10, usage_tokens: 1_000_000, usage_amount: 10 }
const maxTasks = 12
const newTaskKind = ref<QQGrowthTaskKind>('checkin_total')
const form = ref<QQCheckinConfig | null>(null)
const enabledTasks = computed(() => form.value?.growth.tasks.filter(task => task.enabled && rewardEnabled(task.reward)).length ?? 0)
const busy = ref(false)
const error = ref('')
const notice = ref('')

function isAmountKind(kind: QQGrowthTaskKind) { return kind === 'recharge_total' || kind === 'usage_amount' }

function targetLimit(kind: QQGrowthTaskKind) {
  return kind === 'binding' ? 1 : kind === 'usage_count' ? 1_000_000 : kind === 'usage_tokens' ? 1_000_000_000_000_000 : isAmountKind(kind) ? 1_000_000_000 : 3650
}

function addTask() {
  if (!form.value || busy.value || form.value.growth.tasks.length >= maxTasks) return
  const kind = newTaskKind.value
  form.value.growth.tasks.push({ id: 'task_' + crypto.randomUUID().replace(/-/g, ''), name: t('qqbot.growth.kinds.' + kind), kind, target: targets[kind], chances: 1, reward: normalizeGrowthReward(undefined, 1), enabled: false })
}

function validTask(task: QQGrowthTask) {
  return task.name.trim().length > 0 && Array.from(task.name).length <= 40 && !/[\r\n\t]/.test(task.name)
    && kinds.includes(task.kind) && Number.isFinite(task.target) && task.target >= (isAmountKind(task.kind) ? 0.01 : 1)
    && task.target <= targetLimit(task.kind)
    && (isAmountKind(task.kind) ? Number(task.target.toFixed(2)) === task.target : Number.isInteger(task.target))
    && validGrowthReward(task.reward)
}

async function save() {
  if (!form.value || busy.value) return
  error.value = ''; notice.value = ''
  if (form.value.growth.tasks.length > maxTasks || !form.value.growth.tasks.every(validTask)) {
    error.value = t('qqbot.growth.invalidTask')
    return
  }
  busy.value = true
  try { form.value = await saveGrowthSettings(form.value); notice.value = t('common.saved') }
  catch (e) { error.value = (e as Error).message || t('common.error') }
  finally { busy.value = false }
}

onMounted(async () => {
  try { form.value = await loadCheckinConfig() }
  catch (e) { error.value = (e as Error).message || t('common.error') }
})
</script>
