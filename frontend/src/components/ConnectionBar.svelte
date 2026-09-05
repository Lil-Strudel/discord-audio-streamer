<script lang="ts">
  import {
    Connect, Disconnect, JoinChannel, LeaveChannel, ListGuilds, ListVoiceChannels,
  } from "../../wailsjs/go/main/App";
  import type { discord, main } from "../../wailsjs/go/models";
  import type { Telemetry } from "../lib/types";
  import { errorText } from "../lib/types";

  let {
    status,
    telemetry,
    onError,
    onSettings,
  }: {
    status: main.Status;
    telemetry: Telemetry;
    onError: (message: string) => void;
    onSettings: () => void;
  } = $props();

  let guilds = $state<discord.Guild[]>([]);
  let channels = $state<discord.Channel[]>([]);
  let guildID = $state("");
  let channelID = $state("");
  let busy = $state(false);

  async function run(fn: () => Promise<unknown>) {
    busy = true;
    try {
      await fn();
    } catch (err) {
      onError(errorText(err));
    } finally {
      busy = false;
    }
  }

  async function loadGuilds() {
    guilds = (await ListGuilds()) ?? [];
    if (!guilds.some((g) => g.id === guildID)) {
      guildID = (guilds.find((g) => g.id === status.settings.lastGuildId) ?? guilds[0])?.id ?? "";
    }
    await loadChannels();
  }

  async function loadChannels() {
    if (!guildID) {
      channels = [];
      channelID = "";
      return;
    }
    channels = (await ListVoiceChannels(guildID)) ?? [];
    if (!channels.some((c) => c.id === channelID)) {
      channelID =
        (channels.find((c) => c.id === status.settings.lastChannelId) ?? channels[0])?.id ?? "";
    }
  }

  // Guilds arrive with the gateway handshake, so the list is only meaningful
  // once connected, and has to be cleared when the connection goes away.
  $effect(() => {
    if (status.connected) {
      loadGuilds().catch((err) => onError(errorText(err)));
    } else {
      guilds = [];
      channels = [];
    }
  });
</script>

<header>
  <div class="identity">
    {#if status.connected && status.botAvatar}
      <img src={status.botAvatar} alt="" />
    {:else}
      <div class="avatar-placeholder"></div>
    {/if}
    <div class="who">
      <strong>{status.connected ? status.botName : "Not connected"}</strong>
      <span class:live={status.inVoice}>
        {#if status.inVoice}
          {#if telemetry.encryptionReady}
            In voice · encrypted
          {:else}
            In voice · securing…
          {/if}
        {:else if status.connected}
          Connected to Discord
        {:else}
          Press Connect to bring the bot online
        {/if}
      </span>
    </div>
  </div>

  <div class="controls">
    {#if status.connected}
      <select
        bind:value={guildID}
        onchange={() => run(loadChannels)}
        disabled={busy || guilds.length === 0}
      >
        {#each guilds as guild (guild.id)}
          <option value={guild.id}>{guild.name}</option>
        {/each}
        {#if guilds.length === 0}
          <option value="">No servers — invite the bot first</option>
        {/if}
      </select>

      <select bind:value={channelID} disabled={busy || channels.length === 0}>
        {#each channels as channel (channel.id)}
          <option value={channel.id}>
            {channel.stage ? "Stage" : "Voice"} · {channel.name}
          </option>
        {/each}
        {#if channels.length === 0}
          <option value="">No voice channels</option>
        {/if}
      </select>

      {#if status.inVoice}
        <button disabled={busy} onclick={() => run(LeaveChannel)}>Leave</button>
      {:else}
        <button
          class="primary"
          disabled={busy || !channelID}
          onclick={() => run(() => JoinChannel(guildID, channelID))}
        >
          Join
        </button>
      {/if}

      <button disabled={busy} onclick={() => run(Disconnect)}>Disconnect</button>
    {:else}
      <button class="primary" disabled={busy} onclick={() => run(Connect)}>
        {busy ? "Connecting…" : "Connect"}
      </button>
    {/if}

    <button class="icon" title="Settings" onclick={onSettings} aria-label="Settings">⚙</button>
  </div>
</header>

<style>
  header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 20px;
    padding: 12px 20px;
    background: var(--surface);
    border-bottom: 1px solid var(--border);
  }

  .identity {
    display: flex;
    align-items: center;
    gap: 10px;
    min-width: 0;
  }

  .identity img,
  .avatar-placeholder {
    width: 34px;
    height: 34px;
    border-radius: 50%;
    flex: 0 0 auto;
  }

  .avatar-placeholder {
    background: var(--border);
  }

  .who {
    display: flex;
    flex-direction: column;
    line-height: 1.3;
    min-width: 0;
  }

  .who strong {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .who span {
    font-size: 12px;
    color: var(--text-faint);
  }

  .who span.live {
    color: var(--good);
  }

  .controls {
    display: flex;
    align-items: center;
    gap: 8px;
    flex: 0 1 auto;
  }

  .controls select {
    width: auto;
    max-width: 180px;
  }

  .controls button {
    flex: 0 0 auto;
  }

  button.icon {
    padding: 7px 10px;
    font-size: 15px;
    line-height: 1;
  }
</style>
