import { HELPER_DESCRIPTION, HELPER_INSTRUCTIONS } from "./helper-instructions";

const HELPER_AGENT_NAME = "Agora Helper";

/**
 * Skip path, issue 2/2: "Create your first Agora Agent".
 *
 * Companion to install-runtime-issue.ts. The body is a FUNCTION (not a
 * static const) because it needs to embed:
 *   - A mention chip pointing at the install-runtime issue (so the user
 *     can jump to it from this issue) — requires the install-runtime
 *     issue's identifier + uuid, only known after that issue is created.
 *   - The full Helper markdown block in the user's language (so the
 *     embedded ```md fence matches the surrounding body language).
 *
 * Caller MUST create install-runtime first, then call this with its ids.
 */

/**
 * Step 2 of the skip-path bundle. Localized title for supported locales.
 */
export const CREATE_AGENT_GUIDE_ISSUE_TITLE = {
  en: "Step 2 — Create your first Agora Agent",
  zh: "第 2 步 —— 创建你的第一个 Agora Agent",
  uz: "2-qadam — Birinchi Agora Agent'ingizni yarating",
  ru: "Шаг 2 — Создайте своего первого Agora Agent",
} as const;

interface BodyOpts {
  lang: "en" | "zh" | "uz" | "ru";
  installRuntimeIdentifier: string;
  installRuntimeId: string;
}

export function getCreateAgentGuideBody(opts: BodyOpts): string {
  const mention = `[${opts.installRuntimeIdentifier}](mention://issue/${opts.installRuntimeId})`;
  if (opts.lang === "zh") {
    return zhBody(mention);
  }
  if (opts.lang === "uz") {
    return uzBody(mention);
  }
  if (opts.lang === "ru") {
    return ruBody(mention);
  }
  return enBody(mention);
}

function enBody(installRuntimeMention: string): string {
  return `Once your runtime is online (see ${installRuntimeMention}), build your first agent — Agora Helper. The prompt below is pre-written; just copy.

## 1. Open the new-agent screen

Go to **Agents** in the sidebar → click **New Agent**.

## 2. Pick the runtime you just installed

Select the runtime under "Runtime". If nothing shows up, the runtime isn't online yet — finish the install steps in ${installRuntimeMention}.

## 3. Copy each block into the matching field

**Name**
\`\`\`md
${HELPER_AGENT_NAME}
\`\`\`

**Description**
\`\`\`md
${HELPER_DESCRIPTION.en}
\`\`\`

**Instructions**
\`\`\`md
${HELPER_INSTRUCTIONS.en}
\`\`\`

## 4. Save → assign an issue

Hit **Create**. The new agent shows up in the workspace agent list.

Now create an issue (or reassign an existing one) → set assignee = Agora Helper → set status to **todo**. The runtime picks the task up within a few seconds and starts working. Watch progress in the issue's task panel.

## Where to go next

- **Skills** — reusable instruction packs you can attach to any agent.
- **Squads** — groups of agents that can be assigned together.
- **Autopilots** — scheduled or webhook-triggered runs.
- **Docs** — https://agora.dev/docs.`;
}

function zhBody(installRuntimeMention: string): string {
  return `等运行时上线（见 ${installRuntimeMention}）之后，把第一个 agent —— Agora Helper —— 建出来。下面的提示词已经写好，直接复制即可。

## 1. 打开新建 agent 页

在侧边栏点 **Agents** → 点 **New Agent**。

## 2. 选你刚装好的运行时

在 "Runtime" 下选它。如果什么都没有，说明运行时还没上线 —— 先按 ${installRuntimeMention} 把安装步骤走完。

## 3. 把下面三段分别复制到对应字段

**名称**
\`\`\`md
${HELPER_AGENT_NAME}
\`\`\`

**描述**
\`\`\`md
${HELPER_DESCRIPTION.zh}
\`\`\`

**指令**
\`\`\`md
${HELPER_INSTRUCTIONS.zh}
\`\`\`

## 4. 保存 → 分派 issue

点 **Create**。新 agent 会出现在 workspace 的 agent 列表里。

接着创建一个 issue（或把已有 issue 重新分派）→ 把 assignee 设成 Agora Helper → 状态切到 **todo**。运行时会在几秒内接走任务并开始工作。在 issue 的任务面板里看进度。

## 接下来去哪

- **Skills** —— 可复用的指令包，可挂到任何 agent 上。
- **Squads** —— 可一起被分派的一组 agent。
- **Autopilots** —— 定时或 webhook 触发的运行。
- **文档** —— https://agora.dev/docs。`;
}

function uzBody(installRuntimeMention: string): string {
  return `runtime online holatga oʻtgach (${installRuntimeMention} ga qarang), birinchi agent —— Agora Helper —— ni yarating. Quyidagi prompt oldindan yozilgan; shunchaki nusxa oling.

## 1. Yangi agent oynasini oching

Yon panelda **Agents** ga oʻting → **New Agent** ni bosing.

## 2. Endigina oʻrnatgan runtime'ni tanlang

"Runtime" ostida runtime'ni tanlang. Hech narsa koʻrinmasa, runtime hali online emas —— ${installRuntimeMention} dagi oʻrnatish qadamlarini tugating.

## 3. Har bir blokni tegishli maydonga nusxalang

**Name**
\`\`\`md
${HELPER_AGENT_NAME}
\`\`\`

**Description**
\`\`\`md
${HELPER_DESCRIPTION.uz}
\`\`\`

**Instructions**
\`\`\`md
${HELPER_INSTRUCTIONS.uz}
\`\`\`

## 4. Saqlash → issue tayinlash

**Create** ni bosing. Yangi agent workspace agent roʻyxatida paydo boʻladi.

Endi issue yarating (yoki mavjudini qayta tayinlang) → assignee = Agora Helper qiling → statusni **todo** ga oʻtkazing. runtime bir necha soniya ichida vazifani oladi va ishlay boshlaydi. Jarayonni issue'ning task panel'ida kuzating.

## Keyin qayerga borish kerak

- **Skills** —— istalgan agent'ga biriktirsa boʻladigan qayta ishlatiladigan instruction paketlari.
- **Squads** —— birga tayinlanishi mumkin boʻlgan agent guruhlari.
- **Autopilots** —— rejalashtirilgan yoki webhook orqali ishga tushadigan jarayonlar.
- **Docs** —— https://agora.dev/docs.`;
}

function ruBody(installRuntimeMention: string): string {
  return `Как только ваш runtime будет online (см. ${installRuntimeMention}), создайте первого agent —— Agora Helper. Промпт ниже уже написан; просто скопируйте его.

## 1. Откройте экран нового agent

Перейдите в **Agents** на боковой панели → нажмите **New Agent**.

## 2. Выберите только что установленный runtime

Выберите runtime в разделе "Runtime". Если ничего не отображается, значит runtime ещё не online —— завершите шаги установки в ${installRuntimeMention}.

## 3. Скопируйте каждый блок в соответствующее поле

**Name**
\`\`\`md
${HELPER_AGENT_NAME}
\`\`\`

**Description**
\`\`\`md
${HELPER_DESCRIPTION.ru}
\`\`\`

**Instructions**
\`\`\`md
${HELPER_INSTRUCTIONS.ru}
\`\`\`

## 4. Сохраните → назначьте issue

Нажмите **Create**. Новый agent появится в списке agent рабочего пространства.

Теперь создайте issue (или переназначьте существующую) → установите assignee = Agora Helper → переключите статус на **todo**. runtime подхватит задачу за несколько секунд и начнёт работу. Следите за прогрессом в task panel этой issue.

## Куда двигаться дальше

- **Skills** —— переиспользуемые наборы инструкций, которые можно прикрепить к любому agent.
- **Squads** —— группы agent, которые можно назначать вместе.
- **Autopilots** —— запуски по расписанию или по webhook.
- **Docs** —— https://agora.dev/docs.`;
}
