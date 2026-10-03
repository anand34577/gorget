import * as React from "react";
import * as Pop from "@radix-ui/react-popover";
import { cn } from "@/lib/utils";

export interface Suggestion {
  value: string;
  /** Shown instead of the value when set. */
  label?: string;
  /** Section heading; consecutive suggestions with the same group share it. */
  group?: string;
  hint?: string;
}

export type SuggestionLike = string | Suggestion;

export function normalizeSuggestions(list: SuggestionLike[] | undefined): Suggestion[] {
  return (list ?? []).map((s) => (typeof s === "string" ? { value: s } : s));
}

function matches(s: Suggestion, query: string): boolean {
  if (!query) return true;
  const hay = `${s.value} ${s.label ?? ""} ${s.group ?? ""} ${s.hint ?? ""}`.toLowerCase();
  return query
    .toLowerCase()
    .split(/\s+/)
    .filter(Boolean)
    .every((t) => hay.includes(t));
}

interface Props extends Omit<React.InputHTMLAttributes<HTMLInputElement>, "value" | "onChange" | "onSelect"> {
  value: string;
  onChange: (v: string) => void;
  /** Called when a suggestion is chosen; defaults to onChange. */
  onPick?: (v: string) => void;
  /** Called on Enter when no suggestion is highlighted. */
  onEnter?: () => void;
  suggestions: SuggestionLike[];
  mono?: boolean;
  /** Show the whole list when the field is empty and focused. */
  showAllOnFocus?: boolean;
  limit?: number;
}

/**
 * Text input that suggests values as you type. Free text is always allowed; picking a
 * suggestion fills the field (or calls onPick). Works inside dialogs: the list is portaled.
 */
export const SuggestInput = React.forwardRef<HTMLInputElement, Props>(function SuggestInput(
  { value, onChange, onPick, onEnter, suggestions, mono, showAllOnFocus = true, limit = 40, className, disabled, onFocus, onBlur, onKeyDown, ...rest },
  ref,
) {
  const listId = React.useId();
  const [open, setOpen] = React.useState(false);
  const [active, setActive] = React.useState(-1);
  const anchor = React.useRef<HTMLDivElement>(null);
  const items = React.useMemo(() => normalizeSuggestions(suggestions), [suggestions]);
  const shown = React.useMemo(() => {
    if (!value && !showAllOnFocus) return [];
    return items.filter((s) => s.value !== value && matches(s, value)).slice(0, limit);
  }, [items, value, showAllOnFocus, limit]);
  const visible = open && shown.length > 0 && !disabled;

  React.useEffect(() => {
    setActive(-1);
  }, [value, open]);

  const pick = (v: string) => {
    (onPick ?? onChange)(v);
    setOpen(false);
  };

  return (
    <Pop.Root open={visible} onOpenChange={setOpen}>
      <Pop.Anchor asChild>
        <div ref={anchor} className="relative w-full">
          <input
            ref={ref}
            role="combobox"
            aria-expanded={visible}
            aria-controls={visible ? listId : undefined}
            aria-autocomplete="list"
            aria-activedescendant={visible && active >= 0 ? `${listId}-${active}` : undefined}
            autoComplete="off"
            spellCheck={false}
            disabled={disabled}
            value={value}
            onChange={(e) => {
              onChange(e.target.value);
              setOpen(true);
            }}
            onFocus={(e) => {
              setOpen(true);
              onFocus?.(e);
            }}
            onBlur={(e) => {
              onBlur?.(e);
            }}
            onKeyDown={(e) => {
              onKeyDown?.(e);
              if (e.defaultPrevented) return;
              if (e.key === "ArrowDown") {
                e.preventDefault();
                setOpen(true);
                setActive((a) => Math.min(a + 1, shown.length - 1));
              } else if (e.key === "ArrowUp") {
                e.preventDefault();
                setActive((a) => Math.max(a - 1, 0));
              } else if (e.key === "Enter") {
                if (visible && active >= 0 && shown[active]) {
                  e.preventDefault();
                  pick(shown[active].value);
                } else if (onEnter) {
                  e.preventDefault();
                  onEnter();
                }
              } else if (e.key === "Escape" && visible) {
                e.stopPropagation();
                setOpen(false);
              }
            }}
            className={cn(
              "h-8.5 w-full rounded-md border border-line bg-surface px-2.5 text-[13px] text-ink placeholder:text-ink-3 transition-colors",
              "hover:border-line-strong focus:border-blued focus:outline-none focus:ring-2 focus:ring-blued/20 disabled:opacity-60",
              mono && "font-mono",
              className,
            )}
            {...rest}
          />
        </div>
      </Pop.Anchor>
      <Pop.Portal>
        <Pop.Content
          align="start"
          sideOffset={4}
          onOpenAutoFocus={(e) => e.preventDefault()}
          onCloseAutoFocus={(e) => e.preventDefault()}
          onInteractOutside={(e) => {
            if (anchor.current?.contains(e.target as Node)) e.preventDefault();
          }}
          style={{ width: "var(--radix-popover-trigger-width)" }}
          className="z-[60] max-h-60 overflow-y-auto rounded-lg border border-line bg-surface p-1 shadow-xl"
        >
          <ul id={listId} role="listbox" aria-label="Suggestions">
            {shown.map((s, i) => (
              <React.Fragment key={s.value}>
                {s.group && s.group !== shown[i - 1]?.group && <li role="presentation" className="px-2 pb-0.5 pt-1.5 text-[10.5px] font-medium uppercase tracking-wider text-ink-3">{s.group}</li>}
                <li
                  id={`${listId}-${i}`}
                  role="option"
                  aria-selected={i === active}
                  onMouseDown={(e) => e.preventDefault()}
                  onMouseEnter={() => setActive(i)}
                  onClick={() => pick(s.value)}
                  className={cn("flex cursor-pointer items-baseline justify-between gap-3 rounded-md px-2 py-1.5 text-[13px]", i === active ? "bg-sunken" : "hover:bg-sunken")}
                >
                  <span className={cn("truncate", !s.label && mono && "font-mono text-[12.5px]")}>{s.label ?? s.value}</span>
                  {(s.hint || s.label) && <span className="shrink-0 truncate font-mono text-[11px] text-ink-3">{s.hint ?? s.value}</span>}
                </li>
              </React.Fragment>
            ))}
          </ul>
        </Pop.Content>
      </Pop.Portal>
    </Pop.Root>
  );
});
