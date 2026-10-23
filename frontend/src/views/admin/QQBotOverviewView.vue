<template>
  <AppLayout>
    <div class="mx-auto max-w-6xl space-y-6">
      <div class="flex items-center justify-between gap-3"><h1 class="text-2xl font-semibold">{{ t('qqbot.workspace.overview') }}</h1><button class="btn btn-secondary" :disabled="loading" @click="load">{{ t('common.refresh') }}</button></div>
      <p v-if="error" role="alert" class="rounded-lg bg-red-50 p-4 text-red-700">{{ error }}</p>
      <template v-if="overview && config">
        <p class="text-sm text-gray-500 dark:text-gray-400">{{ t('qqbot.workspace.overviewHelp', { day: overview.day }) }}</p>
        <div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
          <article v-for="stat in stats" :key="stat.label" class="card p-5"><p class="text-sm text-gray-500 dark:text-gray-400">{{ t(stat.label) }}</p><p class="mt-2 text-2xl font-semibold">{{ stat.value }}</p></article>
        </div>
        <p v-if="overview.reward_alert" role="alert" class="rounded-lg bg-amber-50 p-4 text-amber-800">{{ t('qqbot.workspace.budgetExceeded') }}</p>
        <section class="card space-y-4 p-5">
          <h2 class="text-lg font-semibold">{{ t('qqbot.workspace.effectiveStatus') }}</h2>
          <p>{{ t(config.enabled ? 'qqbot.workspace.enabled' : 'qqbot.workspace.disabled') }} · {{ t('qqbot.workspace.lastEvent') }}: {{ overview.last_event ? new Date(overview.last_event).toLocaleString() : t('qqbot.workspace.noEvent') }}</p>
          <ul class="grid gap-2 text-sm sm:grid-cols-2"><li v-for="check in overview.checks" :key="check.code" :class="check.ok ? 'text-green-700 dark:text-green-400' : 'text-amber-700 dark:text-amber-400'">{{ check.ok ? '✓' : '○' }} {{ check.message }}</li></ul>
          <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('qqbot.workspace.connectivityHelp') }}</p>
          <div class="flex flex-wrap gap-2"><RouterLink class="btn btn-secondary" to="/admin/qqbot/config">{{ t('qqbot.basicConfig') }}</RouterLink><RouterLink class="btn btn-secondary" to="/admin/qqbot/groups">{{ t('qqbot.workspace.groups') }}</RouterLink><RouterLink class="btn btn-secondary" to="/admin/qqbot/records">{{ t('qqbot.workspace.records') }}</RouterLink></div>
        </section>
        <section class="card space-y-4 p-5">
          <h2 class="text-lg font-semibold">{{ t('qqbot.workspace.diagnose') }}</h2>
          <p class="text-sm text-gray-500 dark:text-gray-400">{{ t('qqbot.workspace.diagnoseHelp') }}</p>
          <form class="flex flex-wrap gap-3" @submit.prevent="diagnose"><select v-model="group" class="input min-w-0 flex-1" required :aria-label="t('qqbot.group')"><option value="">{{ t('qqbot.workspace.chooseGroup') }}</option><option v-for="id in groups" :key="id" :value="id">{{ config.group_policies?.find(p => p.group_id === id)?.label || id }}</option></select><input v-model.trim="member" class="input min-w-0 flex-1" maxlength="128" :placeholder="t('qqbot.workspace.optionalMember')" :aria-label="t('qqbot.workspace.memberID')" /><button class="btn btn-primary" :disabled="diagnosing">{{ t('qqbot.workspace.diagnoseAction') }}</button></form>
          <template v-if="diagnosis"><ul class="space-y-2 text-sm"><li v-for="check in diagnosis.checks" :key="check.code">{{ check.ok ? '✓' : '○' }} {{ check.message }}</li></ul><article v-for="job in diagnosis.jobs" :key="job.id" class="rounded border p-3 text-sm dark:border-dark-700"><p>{{ new Date(job.created_at).toLocaleString() }} · {{ job.kind }} · {{ job.status }}</p><p class="break-words [overflow-wrap:anywhere]">{{ job.decision_reason || job.error || job.trigger_reason }}</p></article><p v-if="!diagnosis.jobs.length" class="text-sm text-gray-500 dark:text-gray-400">{{ t('qqbot.workspace.noJobs') }}</p></template>
        </section>
      </template>
    </div>
  </AppLayout>
</template>
<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import { qqbotAPI, type QQBotConfig, type QQBotRuntime, type QQOverview, type QQDiagnosticCheck } from '@/api/qqbot'
const { t } = useI18n()
const config = ref<QQBotConfig>(), overview = ref<QQOverview>(), runtime = ref<QQBotRuntime>()
const loading = ref(false), error = ref(''), diagnosing = ref(false), group = ref(''), member = ref('')
const diagnosis = ref<{ checks: QQDiagnosticCheck[]; jobs: QQBotRuntime['jobs'] }>()
const groups = computed(() => [...new Set([...(config.value?.allowed_groups || []), ...(runtime.value?.groups.map(g => g.id) || [])])])
const stats = computed(() => overview.value ? [
 { label: 'qqbot.workspace.chatRemaining', value: `${overview.value.chat_remaining} / ${config.value?.total_daily_chat_limit}` },
 { label: 'qqbot.workspace.imageRemaining', value: `${overview.value.image_remaining} / ${config.value?.total_daily_image_limit}` },
 { label: 'qqbot.workspace.todayFailures', value: overview.value.failed },
 { label: 'qqbot.workspace.pendingRewards', value: overview.value.pending_rewards },
 { label: 'qqbot.workspace.todayRewards', value: `$${overview.value.reward_amount.toFixed(2)}` },
 { label: 'qqbot.workspace.queue', value: `${overview.value.queued} / ${overview.value.running}` },
 { label: 'qqbot.workspace.chatUsed', value: overview.value.chat_used },
 { label: 'qqbot.workspace.imageUsed', value: overview.value.image_used }
] : [])
async function load() { loading.value = true; error.value = ''; try { [config.value, overview.value, runtime.value] = await Promise.all([qqbotAPI.config(), qqbotAPI.overview(), qqbotAPI.runtime()]) } catch (e) { error.value = (e as Error).message } finally { loading.value = false } }
async function diagnose() { diagnosing.value = true; error.value = ''; diagnosis.value = undefined; try { diagnosis.value = await qqbotAPI.diagnose(group.value, member.value) } catch (e) { error.value = (e as Error).message } finally { diagnosing.value = false } }
onMounted(load)
</script>
