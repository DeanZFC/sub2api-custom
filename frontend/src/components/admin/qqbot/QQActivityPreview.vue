<template>
  <section class="card space-y-4 p-5">
    <h2 class="text-lg font-semibold">{{ t('qqbot.workspace.preview') }}</h2><p class="text-sm text-gray-500">{{ t('qqbot.workspace.previewHelp') }}</p>
    <form class="flex flex-wrap gap-3" @submit.prevent="load"><input v-model.number="userID" type="number" min="1" step="1" required class="input min-w-0 flex-1 basis-48" :aria-label="t('qqbot.workspace.userID')" :placeholder="t('qqbot.workspace.userID')" /><button class="btn btn-primary" :disabled="loading">{{ t('qqbot.workspace.preview') }}</button></form>
    <p v-if="error" role="alert" class="text-red-600">{{ error }}</p>
    <div v-if="data" class="space-y-3 text-sm">
      <p>{{ t('qqbot.workspace.bindingState') }}: {{ yes(data.bound) }}</p>
      <p>{{ t('qqbot.checkinTitle') }}: {{ yes(data.checkin_eligible) }} · {{ data.checkin_rule }} · {{ t('qqbot.workspace.alreadyCheckedIn') }}: {{ yes(data.checkin_done) }}</p>
      <p>{{ t('qqbot.inviteTitle') }}: {{ yes(data.invite_eligible) }} · {{ data.invite_rule }}</p>
      <p>{{ t('qqbot.workspace.lotteryRemaining') }}: {{ data.lottery.remaining }} · {{ t('qqbot.workspace.untilGuarantee') }}: {{ data.lottery.draws_until_guarantee }}</p>
      <p>{{ t('qqbot.workspace.expectedCheckin') }}: ${{ data.checkin_expected.toFixed(6) }} · {{ t('qqbot.workspace.expectedLottery') }}: ${{ data.lottery_expected.toFixed(6) }} · {{ t('qqbot.workspace.expectedNext') }}: ${{ data.lottery_next_expected.toFixed(6) }}</p>
      <article v-for="task in data.tasks" :key="task.id" class="rounded border p-3 dark:border-dark-700"><strong>{{ task.name }}</strong><p>{{ task.progress }} · {{ task.status || yes(task.eligible) }} · {{ task.reward }}</p></article>
    </div>
  </section>
</template>
<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { qqbotAPI, type QQActivityPreview } from '@/api/qqbot'
const { t } = useI18n()
const userID = ref<number>(), loading = ref(false), error = ref(''), data = ref<QQActivityPreview>()
const yes = (value: boolean) => t(value ? 'qqbot.workspace.yes' : 'qqbot.workspace.no')
async function load() { if (!userID.value) return; loading.value = true; error.value = ''; data.value = undefined; try { data.value = await qqbotAPI.activityPreview(userID.value) } catch (e) { error.value = (e as Error).message } finally { loading.value = false } }
</script>
