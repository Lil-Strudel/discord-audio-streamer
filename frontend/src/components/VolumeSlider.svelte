<script lang="ts">
  import { SetVolume } from "../../wailsjs/go/main/App";

  let {
    value = 100,
    disabled = false,
    id = "volume",
    label = "Volume",
    // The output volume unless told otherwise; a soundboard track passes its own.
    onchange = SetVolume,
  }: {
    value?: number;
    disabled?: boolean;
    id?: string;
    label?: string;
    onchange?: (percent: number) => Promise<unknown>;
  } = $props();

  // `requested` is the last position we asked the backend for. The slider shows
  // it in preference to the prop until the backend reports that same value back,
  // which closes the window between letting go of the knob and the status event
  // arriving — the window in which the knob used to snap back to the old value.
  let requested = $state<number | null>(null);
  let local = $derived(requested ?? value);

  $effect(() => {
    if (requested !== null && value === requested) requested = null;
  });

  async function push(v: number) {
    requested = v;
    try {
      await onchange(v);
    } catch {
      // Volume is applied to the live stream and persisted separately; a failed
      // write is not worth interrupting playback over.
    }
  }
</script>

<div class="volume">
  <label for={id}>{label}</label>
  <input
    {id}
    type="range"
    min="0"
    max="150"
    step="1"
    {disabled}
    value={local}
    oninput={(e) => push(Number(e.currentTarget.value))}
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
