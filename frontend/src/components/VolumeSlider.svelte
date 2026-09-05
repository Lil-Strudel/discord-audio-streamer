<script lang="ts">
  import { SetVolume } from "../../wailsjs/go/main/App";

  let { value = 100, disabled = false } = $props();

  // Only the in-flight drag position is held locally. Seeding state from the
  // prop would freeze it at the initial value; deriving instead means a volume
  // restored from settings appears immediately, without stuttering under a drag.
  let dragged = $state<number | null>(null);
  let dragging = $state(false);

  let local = $derived(dragging && dragged !== null ? dragged : value);

  async function push(v: number) {
    dragged = v;
    try {
      await SetVolume(v);
    } catch {
      // Volume is applied to the live stream and persisted separately; a failed
      // write is not worth interrupting playback over.
    }
  }
</script>

<div class="volume">
  <label for="volume">Volume</label>
  <input
    id="volume"
    type="range"
    min="0"
    max="150"
    step="1"
    {disabled}
    value={local}
    oninput={(e) => push(Number(e.currentTarget.value))}
    onpointerdown={() => (dragging = true)}
    onpointerup={() => { dragging = false; dragged = null; }}
    onpointercancel={() => { dragging = false; dragged = null; }}
  />
  <span class="value" class:boosted={local > 100}>{Math.round(local)}%</span>
</div>

<style>
  .volume {
    display: grid;
    grid-template-columns: auto 1fr 48px;
    align-items: center;
    gap: 12px;
  }

  label {
    color: var(--text-dim);
    font-size: 13px;
  }

  .value {
    text-align: right;
    font-variant-numeric: tabular-nums;
    color: var(--text-dim);
    font-size: 13px;
  }

  /* Above 100% the signal is amplified and loud material will clip, so the
     number stops looking like a neutral setting. */
  .value.boosted {
    color: var(--warn);
  }
</style>
