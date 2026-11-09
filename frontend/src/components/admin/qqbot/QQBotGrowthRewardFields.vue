<template>
  <div class="space-y-3">
    <div class="grid gap-4 sm:grid-cols-2">
      <label class="block space-y-1">{{ t('qqbot.growth.rewardType') }}
        <select v-model="reward.type" :data-testid="fieldId('reward-type')" class="input" @change="changeType">
          <option v-for="kind in allowedTypes ?? types" :key="kind" :value="kind">{{ kind === 'coupon' ? t('coupon.adminTitle') : t('qqbot.redemption.types.' + kind) }}</option>
        </select>
      </label>
      <label v-if="reward.type === 'balance'" class="block space-y-1">{{ t('qqbot.growth.rewardAmount') }}<input v-model.number="reward.amount" :data-testid="fieldId('reward-amount')" type="number" min="0" max="1000" step="0.00000001" required class="input" /></label>
      <label v-else class="block space-y-1">{{ t(reward.type === 'chances' ? 'qqbot.growth.chances' : 'qqbot.growth.rewardQuantity') }}<input v-model.number="reward.quantity" :data-testid="fieldId(reward.type === 'chances' ? 'chances' : 'reward-quantity')" type="number" min="0" :max="reward.type === 'coupon' ? 100 : 1000" step="1" required class="input" /></label>
      <label v-if="reward.type === 'coupon'" class="block space-y-1">{{ t('coupon.template') }}<CouponTemplateSelect v-model="reward.coupon_template_id" /></label>
      <template v-if="reward.type === 'physical' || reward.type === 'other'">
        <label class="block space-y-1">{{ t('qqbot.redemption.prize') }}<input v-model="reward.prize_name" :data-testid="fieldId('prize-name')" maxlength="100" :required="reward.quantity > 0" class="input" /></label>
        <label class="block space-y-1">{{ t('qqbot.redemption.instructions') }}<textarea v-model="reward.claim_instructions" :data-testid="fieldId('instructions')" maxlength="500" rows="2" class="input" /></label>
      </template>
    </div>
    <p v-if="reward.type === 'coupon'" class="text-sm text-gray-500">{{ t('coupon.costHint') }}</p>
    <p class="text-sm text-gray-500">{{ t(reward.type === 'coupon' ? 'coupon.qqHelp' : 'qqbot.growth.rewardHelp') }}</p>
  </div>
</template>
<script setup lang="ts">
import CouponTemplateSelect from './CouponTemplateSelect.vue'
import { useI18n } from 'vue-i18n'
import type { QQGrowthReward, QQRewardType } from '@/api/qqbot'
const { t } = useI18n()
const reward = defineModel<QQGrowthReward>({ required: true })
const props = defineProps<{ prefix: string; index?: number; allowedTypes?: QQRewardType[] }>()
const types: QQRewardType[] = ['chances', 'balance', 'physical', 'other', 'coupon']
function fieldId(name: string) { return props.prefix + '-' + name + (props.index === undefined ? '' : '-' + props.index) }
function changeType() {
  reward.value = { type: reward.value.type, amount: 0, quantity: ['physical', 'other', 'coupon'].includes(reward.value.type) ? 1 : 0, prize_name: '', claim_instructions: '' }
}
</script>
