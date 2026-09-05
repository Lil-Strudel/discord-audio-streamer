<script lang="ts">
  import { SaveToken, OpenURL, ValidateToken } from "../../wailsjs/go/main/App";
  import type { discord } from "../../wailsjs/go/models";
  import { errorText } from "../lib/types";

  /**
   * The same component serves first-run onboarding and later reconfiguration,
   * which is why the heading and the finish button are props rather than
   * hard-coded: the steps a user needs are identical in both cases.
   */
  let {
    heading = "Set up your bot",
    finishLabel = "Finish setup",
    onDone = () => {},
    onCancel = null as null | (() => void),
  } = $props();

  let token = $state("");
  let checking = $state(false);
  let saving = $state(false);
  let bot = $state<discord.BotInfo | null>(null);
  let error = $state("");

  const portalURL = "https://discord.com/developers/applications";

  async function check() {
    if (!token.trim()) return;
    checking = true;
    error = "";
    bot = null;
    try {
      bot = await ValidateToken(token.trim());
    } catch (err) {
      error = errorText(err);
    } finally {
      checking = false;
    }
  }

  async function save() {
    saving = true;
    error = "";
    try {
      await SaveToken(token.trim());
      onDone();
    } catch (err) {
      error = errorText(err);
    } finally {
      saving = false;
    }
  }
</script>

<div class="wizard">
  <h1>{heading}</h1>
  <p class="lead">
    This app has no bot of its own — you bring your own, so the bot appears in your
    server under your name and nobody else can use it.
  </p>

  <ol class="steps">
    <li>
      <h2>Create an application</h2>
      <p>
        Open the Discord developer portal and press <strong>New Application</strong>.
        Give it any name; that name is what people will see in the voice channel.
      </p>
      <button onclick={() => OpenURL(portalURL)}>Open the developer portal</button>
    </li>

    <li>
      <h2>Copy the bot token</h2>
      <p>
        In your new application, open the <strong>Bot</strong> tab and press
        <strong>Reset Token</strong>, then copy the value it shows you. Discord only
        displays a token once, so copy it before leaving the page.
      </p>
      <p class="note">
        You do not need to enable any privileged intents. This app only needs to see
        your servers' channels and join voice.
      </p>
    </li>

    <li>
      <h2>Paste it here</h2>
      <div class="token-row">
        <input
          type="password"
          placeholder="Paste the bot token"
          bind:value={token}
          oninput={() => { bot = null; error = ""; }}
          onkeydown={(e) => e.key === "Enter" && check()}
        />
        <button onclick={check} disabled={checking || !token.trim()}>
          {checking ? "Checking…" : "Check"}
        </button>
      </div>
      <p class="note">
        The token is stored on this computer only, encrypted so that another Windows
        account cannot read it. It is never sent anywhere except Discord.
      </p>

      {#if error}
        <p class="error">{error}</p>
      {/if}

      {#if bot}
        <div class="bot-card">
          {#if bot.avatarUrl}
            <img src={bot.avatarUrl} alt="" />
          {/if}
          <div>
            <strong>{bot.name}</strong>
            <span>Token accepted</span>
          </div>
        </div>
      {/if}
    </li>

    <li class:disabled={!bot}>
      <h2>Add the bot to your server</h2>
      <p>
        This link asks only for the three permissions the app needs: view channels,
        connect to voice, and speak.
      </p>
      <button disabled={!bot} onclick={() => bot && OpenURL(bot.inviteUrl)}>
        Invite the bot to a server
      </button>
    </li>
  </ol>

  <div class="actions">
    {#if onCancel}
      <button onclick={onCancel}>Cancel</button>
    {/if}
    <button class="primary" disabled={!bot || saving} onclick={save}>
      {saving ? "Saving…" : finishLabel}
    </button>
  </div>
</div>

<style>
  .wizard {
    max-width: 620px;
    margin: 0 auto;
    padding: 32px 24px 48px;
  }

  h1 {
    margin: 0 0 8px;
    font-size: 24px;
  }

  .lead {
    margin: 0 0 28px;
    color: var(--text-dim);
  }

  .steps {
    list-style: none;
    counter-reset: step;
    margin: 0;
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: 20px;
  }

  .steps li {
    counter-increment: step;
    position: relative;
    padding: 16px 18px 18px 52px;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius);
  }

  .steps li::before {
    content: counter(step);
    position: absolute;
    left: 16px;
    top: 16px;
    width: 24px;
    height: 24px;
    display: grid;
    place-items: center;
    border-radius: 50%;
    background: var(--accent);
    color: #fff;
    font-size: 13px;
    font-weight: 700;
  }

  .steps li.disabled {
    opacity: 0.5;
  }

  .steps li.disabled::before {
    background: var(--border-strong);
  }

  h2 {
    margin: 0 0 6px;
    font-size: 15px;
    font-weight: 700;
  }

  p {
    margin: 0 0 12px;
    color: var(--text-dim);
  }

  .note {
    font-size: 12.5px;
    color: var(--text-faint);
    margin-bottom: 0;
  }

  .token-row {
    display: flex;
    gap: 8px;
    margin-bottom: 10px;
  }

  .token-row button {
    flex: 0 0 auto;
  }

  .error {
    margin: 10px 0 0;
    color: var(--bad);
  }

  .bot-card {
    display: flex;
    align-items: center;
    gap: 12px;
    margin-top: 12px;
    padding: 10px 12px;
    background: var(--surface-raised);
    border: 1px solid color-mix(in srgb, var(--good) 40%, var(--border));
    border-radius: var(--radius-sm);
  }

  .bot-card img {
    width: 36px;
    height: 36px;
    border-radius: 50%;
  }

  .bot-card div {
    display: flex;
    flex-direction: column;
    line-height: 1.3;
  }

  .bot-card span {
    font-size: 12.5px;
    color: var(--good);
  }

  .actions {
    display: flex;
    justify-content: flex-end;
    gap: 10px;
    margin-top: 28px;
  }
</style>
