export interface XtermHandle {
  write(bytes: Uint8Array): void;
  fit(): void;
  focus(): void;
  /** Clear the grid + scrollback (raw_replay full-snapshot replace). */
  reset(): void;
  setFontSize(size: number): void;
  submit(text: string): void;
}

export interface XtermViewProps {
  onData?: (bytes: Uint8Array) => void;
  onResize?: (cols: number, rows: number) => void;
  onReady?: () => void;
  onError?: (message: string) => void;
  onScreen?: (text: string) => void;
  /** Terminal background (defaults to the glasses dark surface). */
  background?: string;
  /** Default foreground. */
  foreground?: string;
  /** Cursor + selection accent (defaults to the indigo accent). */
  cursor?: string;
  fontSize?: number;
  /** Existing tmux panes keep their desktop dimensions; scroll locally. */
  columns?: number;
  rows?: number;
  style?: object;
}
