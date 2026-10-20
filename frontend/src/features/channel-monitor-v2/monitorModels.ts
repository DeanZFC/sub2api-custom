// Compare recognizable generations within a model family. This is a best-effort
// ordering, not a release-date catalog; unrelated and unknown names sort stably.
function modelGeneration(model: string): { family: string; version: number[]; date: number } | null {
  const name = model.toLowerCase()
  const date = Number(name.match(/(?:^|[-_])(20\d{6})(?:$|[-_])/)?.[1] || 0)
  const patterns: Array<[string, RegExp]> = [
    ['gpt', /gpt[-_](\d+(?:[.-]\d{1,2}(?!\d))?)/],
    ['claude', /claude[-_](?:(?:opus|sonnet|haiku)[-_])?(\d+(?:[.-]\d{1,2}(?!\d))?)/],
    ['gemini', /gemini[-_](\d+(?:\.\d+)?)/],
    ['grok', /grok[-_](\d+(?:\.\d+)?)/],
    ['deepseek-v', /deepseek[-_]v(\d+(?:\.\d+)?)/],
    ['deepseek-r', /deepseek[-_]r(\d+(?:\.\d+)?)/],
    ['qwen', /qwen[-_]?(\d+(?:\.\d+)?)/],
    ['o', /^o(\d+)(?:[-_]|$)/],
  ]
  for (const [family, pattern] of patterns) {
    const version = name.match(pattern)?.[1]
    if (version) return { family, version: version.split(/[.-]/).map(Number), date }
  }
  return null
}

export function compareMonitorModels(a: string, b: string): number {
  const left = modelGeneration(a)
  const right = modelGeneration(b)
  // Use the family as a consistent primary key to keep the comparator transitive.
  const leftFamily = left?.family || a.toLowerCase()
  const rightFamily = right?.family || b.toLowerCase()
  if (leftFamily !== rightFamily) return leftFamily.localeCompare(rightFamily)
  if (left && right) {
    for (let i = 0; i < Math.max(left.version.length, right.version.length); i++) {
      const difference = (right.version[i] || 0) - (left.version[i] || 0)
      if (difference) return difference
    }
    if (left.date !== right.date) return right.date - left.date
  }
  return a.localeCompare(b, undefined, { numeric: true })
}

export function monitorModelOptions(
  models: readonly { value: string; label: string; platform?: string }[],
  platforms: readonly string[],
): { value: string; label: string }[] {
  const selected = new Set(platforms)
  const options = new Map<string, { value: string; label: string }>()
  for (const model of models) {
    if (selected.size && model.platform && !selected.has(model.platform)) continue
    options.set(model.value, { value: model.value, label: model.label || model.value })
  }
  return [...options.values()].sort((a, b) => compareMonitorModels(a.value, b.value))
}
