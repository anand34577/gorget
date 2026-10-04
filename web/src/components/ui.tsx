import * as React from "react";
import * as DialogP from "@radix-ui/react-dialog";
import * as AlertP from "@radix-ui/react-alert-dialog";
import * as SwitchP from "@radix-ui/react-switch";
import * as TabsP from "@radix-ui/react-tabs";
import * as MenuP from "@radix-ui/react-dropdown-menu";
import * as TipP from "@radix-ui/react-tooltip";
import { cva, type VariantProps } from "class-variance-authority";
import { Check, Copy, Loader2, X } from "lucide-react";
import { toast } from "sonner";
import { cn, copy } from "@/lib/utils";
import { SuggestInput, type SuggestionLike } from "./suggest";

// ---------- Button ----------

const buttonVariants = cva(
  "inline-flex items-center justify-center gap-1.5 whitespace-nowrap rounded-md text-[13px] font-medium transition-colors disabled:pointer-events-none disabled:opacity-50 [&_svg]:size-4 [&_svg]:shrink-0",
  {
    variants: {
      variant: {
        primary: "bg-blued text-blued-ink hover:bg-blued/90 shadow-sm",
        secondary: "bg-surface text-ink border border-line hover:bg-surface-2 hover:border-line-strong",
        ghost: "text-ink-2 hover:bg-sunken hover:text-ink",
        danger: "bg-oxide text-white hover:bg-oxide/90",
        "danger-ghost": "text-oxide hover:bg-oxide-soft",
      },
      size: { sm: "h-7 px-2.5", md: "h-8.5 px-3.5", lg: "h-10 px-5 text-sm", icon: "size-8" },
    },
    defaultVariants: { variant: "secondary", size: "md" },
  },
);

export interface ButtonProps extends React.ButtonHTMLAttributes<HTMLButtonElement>, VariantProps<typeof buttonVariants> {
  loading?: boolean;
}

export const Button = React.forwardRef<HTMLButtonElement, ButtonProps>(({ className, variant, size, loading, children, disabled, ...props }, ref) => (
  <button ref={ref} className={cn(buttonVariants({ variant, size }), className)} disabled={disabled || loading} {...props}>
    {loading && <Loader2 className="animate-spin" />}
    {children}
  </button>
));
Button.displayName = "Button";

// ---------- Inputs ----------

export const Input = React.forwardRef<HTMLInputElement, React.InputHTMLAttributes<HTMLInputElement>>(({ className, ...props }, ref) => (
  <input
    ref={ref}
    className={cn(
      "h-8.5 w-full rounded-md border border-line bg-surface px-2.5 text-[13px] text-ink placeholder:text-ink-3 transition-colors",
      "hover:border-line-strong focus:border-blued focus:outline-none focus:ring-2 focus:ring-blued/20 disabled:opacity-60",
      className,
    )}
    {...props}
  />
));
Input.displayName = "Input";

export const Textarea = React.forwardRef<HTMLTextAreaElement, React.TextareaHTMLAttributes<HTMLTextAreaElement>>(({ className, ...props }, ref) => (
  <textarea
    ref={ref}
    className={cn(
      "w-full rounded-md border border-line bg-surface px-2.5 py-2 text-[13px] text-ink placeholder:text-ink-3",
      "hover:border-line-strong focus:border-blued focus:outline-none focus:ring-2 focus:ring-blued/20",
      className,
    )}
    {...props}
  />
));
Textarea.displayName = "Textarea";

export function Select({ className, children, ...props }: React.SelectHTMLAttributes<HTMLSelectElement>) {
  return (
    <select
      className={cn(
        "h-8.5 w-full rounded-md border border-line bg-surface px-2 text-[13px] text-ink hover:border-line-strong focus:border-blued focus:outline-none focus:ring-2 focus:ring-blued/20",
        className,
      )}
      {...props}
    >
      {children}
    </select>
  );
}

/** Label + control + hint. A single input/select/textarea child is linked to the label and hint automatically. */
export function Field({ label, hint, error, children, className, htmlFor }: { label: React.ReactNode; hint?: React.ReactNode; error?: string; children: React.ReactNode; className?: string; htmlFor?: string }) {
  const auto = React.useId();
  const hintId = `${auto}-hint`;
  let id = htmlFor;
  let control = children;
  if (React.isValidElement<{ id?: string; "aria-describedby"?: string }>(children) && (children.type === Input || children.type === Select || children.type === Textarea || children.type === "input" || children.type === "select" || children.type === "textarea")) {
    id = children.props.id ?? htmlFor ?? auto;
    control = React.cloneElement(children, { id, "aria-describedby": hint || error ? hintId : undefined });
  }
  return (
    <div className={cn("space-y-1.5", className)}>
      <label htmlFor={id} className="block text-[13px] font-medium text-ink">
        {label}
      </label>
      {control}
      {error ? (
        <p id={hintId} className="text-xs text-oxide">
          {error}
        </p>
      ) : hint ? (
        <p id={hintId} className="text-xs text-ink-3">
          {hint}
        </p>
      ) : null}
    </div>
  );
}

export function Switch({ checked, onCheckedChange, disabled, id, label }: { checked: boolean; onCheckedChange: (v: boolean) => void; disabled?: boolean; id?: string; label?: string }) {
  return (
    <SwitchP.Root
      id={id}
      aria-label={label}
      checked={checked}
      onCheckedChange={onCheckedChange}
      disabled={disabled}
      className="relative h-5 w-9 shrink-0 rounded-full bg-line-strong transition-colors data-[state=checked]:bg-blued disabled:opacity-50"
    >
      <SwitchP.Thumb className="block size-4 translate-x-0.5 rounded-full bg-white shadow transition-transform data-[state=checked]:translate-x-[18px]" />
    </SwitchP.Root>
  );
}

/** A labelled toggle row used throughout settings. */
export function ToggleRow({ title, description, checked, onChange, disabled }: { title: string; description?: React.ReactNode; checked: boolean; onChange: (v: boolean) => void; disabled?: boolean }) {
  const id = React.useId();
  return (
    <div className="flex items-start justify-between gap-6 py-3">
      <label htmlFor={id} className="cursor-pointer">
        <div className="text-[13px] font-medium">{title}</div>
        {description && <div className="mt-0.5 text-xs text-ink-3">{description}</div>}
      </label>
      <Switch id={id} checked={checked} onCheckedChange={onChange} disabled={disabled} />
    </div>
  );
}

export function Checkbox({ checked, onChange, label }: { checked: boolean; onChange: (v: boolean) => void; label: React.ReactNode }) {
  return (
    <label className="flex cursor-pointer items-center gap-2 text-[13px]">
      <input type="checkbox" className="size-4 accent-[var(--blued)]" checked={checked} onChange={(e) => onChange(e.target.checked)} />
      {label}
    </label>
  );
}

// ---------- Surfaces ----------

export function Panel({ className, ...props }: React.HTMLAttributes<HTMLDivElement>) {
  return <div className={cn("rounded-lg border border-line bg-surface shadow-panel", className)} {...props} />;
}

export function PanelHeader({ title, description, actions, className }: { title: React.ReactNode; description?: React.ReactNode; actions?: React.ReactNode; className?: string }) {
  return (
    <div className={cn("flex items-start justify-between gap-4 border-b border-line px-5 py-3.5", className)}>
      <div>
        <h2 className="text-sm font-semibold">{title}</h2>
        {description && <p className="mt-0.5 text-xs text-ink-3">{description}</p>}
      </div>
      {actions && <div className="flex shrink-0 items-center gap-2">{actions}</div>}
    </div>
  );
}

export function PageHeader({ title, description, actions }: { title: string; description?: React.ReactNode; actions?: React.ReactNode }) {
  return (
    <div className="mb-6 flex flex-wrap items-end justify-between gap-4">
      <div>
        <h1 className="font-display text-[28px] font-semibold leading-tight">{title}</h1>
        {description && <p className="mt-1 max-w-2xl text-[13px] text-ink-2">{description}</p>}
      </div>
      {actions && <div className="flex flex-wrap items-center gap-2">{actions}</div>}
    </div>
  );
}

// ---------- Status ----------

const badgeVariants = cva("inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-[11px] font-medium leading-4 whitespace-nowrap", {
  variants: {
    tone: {
      neutral: "bg-sunken text-ink-2",
      blued: "bg-blued-soft text-blued",
      ok: "bg-verdigris-soft text-verdigris",
      warn: "bg-straw-soft text-straw",
      danger: "bg-oxide-soft text-oxide",
    },
  },
  defaultVariants: { tone: "neutral" },
});

export function Badge({ tone, className, ...props }: React.HTMLAttributes<HTMLSpanElement> & VariantProps<typeof badgeVariants>) {
  return <span className={cn(badgeVariants({ tone }), className)} {...props} />;
}

export function Tag({ children }: { children: React.ReactNode }) {
  return <span className="inline-flex items-center rounded border border-line bg-surface-2 px-1.5 font-mono text-[11px] leading-[18px] text-ink-2">{children}</span>;
}

export function StatusDot({ online, pending, className }: { online: boolean; pending?: boolean; className?: string }) {
  return (
    <span
      className={cn("inline-block size-2 shrink-0 rounded-full", pending ? "bg-straw" : online ? "bg-verdigris shadow-[0_0_0_3px] shadow-verdigris/20" : "bg-line-strong", className)}
      role="img"
      aria-label={pending ? "Pending" : online ? "Online" : "Offline"}
    />
  );
}

export function Spinner({ className }: { className?: string }) {
  return <Loader2 className={cn("size-4 animate-spin text-ink-3", className)} />;
}

export function Skeleton({ className }: { className?: string }) {
  return <div className={cn("animate-pulse rounded bg-sunken", className)} />;
}

export function EmptyState({ icon, title, children, action }: { icon?: React.ReactNode; title: string; children?: React.ReactNode; action?: React.ReactNode }) {
  return (
    <div className="flex flex-col items-center justify-center px-6 py-14 text-center">
      {icon && <div className="mb-3 rounded-lg border border-line bg-surface-2 p-3 text-ink-3 [&_svg]:size-5">{icon}</div>}
      <h3 className="text-sm font-semibold">{title}</h3>
      {children && <p className="mt-1 max-w-sm text-[13px] text-ink-3">{children}</p>}
      {action && <div className="mt-4">{action}</div>}
    </div>
  );
}

export function ErrorNote({ children }: { children: React.ReactNode }) {
  return <div className="rounded-md border border-oxide/30 bg-oxide-soft px-3 py-2 text-[13px] text-oxide">{children}</div>;
}

export function Note({ tone = "blued", children }: { tone?: "blued" | "warn"; children: React.ReactNode }) {
  return (
    <div className={cn("rounded-md border px-3 py-2 text-[13px]", tone === "warn" ? "border-straw/30 bg-straw-soft text-ink" : "border-blued/25 bg-blued-soft text-ink")}>
      {children}
    </div>
  );
}

// ---------- Code ----------

export function Mono({ children, className }: { children: React.ReactNode; className?: string }) {
  return <span className={cn("font-mono text-[12.5px]", className)}>{children}</span>;
}

export function CopyButton({ value, label = "Copy", className }: { value: string; label?: string; className?: string }) {
  const [done, setDone] = React.useState(false);
  return (
    <Button
      type="button"
      size="sm"
      variant="ghost"
      className={className}
      aria-label={label}
      onClick={async () => {
        await copy(value);
        setDone(true);
        toast.success("Copied to clipboard");
        setTimeout(() => setDone(false), 1500);
      }}
    >
      {done ? <Check /> : <Copy />}
    </Button>
  );
}

export function SecretBox({ value, caption }: { value: string; caption?: string }) {
  return (
    <div className="space-y-1.5">
      <div className="flex items-center gap-2 rounded-md border border-line bg-sunken px-3 py-2">
        <code className="flex-1 break-all font-mono text-[12.5px]">{value}</code>
        <CopyButton value={value} />
      </div>
      {caption && <p className="text-xs text-ink-3">{caption}</p>}
    </div>
  );
}

// ---------- Table ----------

export function Table({ children, className }: { children: React.ReactNode; className?: string }) {
  return (
    <div className={cn("overflow-x-auto", className)}>
      <table className="w-full min-w-[620px] border-collapse text-left text-[13px]">{children}</table>
    </div>
  );
}

export function Th({ children, className }: { children?: React.ReactNode; className?: string }) {
  return <th className={cn("border-b border-line bg-surface-2 px-4 py-2 text-[11px] font-medium uppercase tracking-wider text-ink-3", className)}>{children}</th>;
}

export function Td({ children, className, ...props }: React.TdHTMLAttributes<HTMLTableCellElement>) {
  return (
    <td className={cn("border-b border-line px-4 py-2.5 align-middle", className)} {...props}>
      {children}
    </td>
  );
}

// ---------- Dialogs ----------

export function Dialog({
  open,
  onOpenChange,
  title,
  description,
  children,
  footer,
  wide,
}: {
  open: boolean;
  onOpenChange: (v: boolean) => void;
  title: string;
  description?: React.ReactNode;
  children: React.ReactNode;
  footer?: React.ReactNode;
  wide?: boolean;
}) {
  return (
    <DialogP.Root open={open} onOpenChange={onOpenChange}>
      <DialogP.Portal>
        <DialogP.Overlay className="fixed inset-0 z-40 bg-[#0b0e12]/50 backdrop-blur-[2px] data-[state=open]:animate-[fade_120ms]" />
        <DialogP.Content
          className={cn(
            "fixed left-1/2 top-[8vh] z-50 flex max-h-[84vh] w-[calc(100vw-32px)] -translate-x-1/2 flex-col rounded-xl border border-line bg-surface shadow-2xl",
            wide ? "max-w-3xl" : "max-w-lg",
          )}
        >
          <div className="flex items-start justify-between gap-4 border-b border-line px-5 py-4">
            <div>
              <DialogP.Title className="text-[15px] font-semibold">{title}</DialogP.Title>
              {description ? (
                <DialogP.Description className="mt-1 text-[13px] text-ink-2">{description}</DialogP.Description>
              ) : (
                <DialogP.Description className="sr-only">{title}</DialogP.Description>
              )}
            </div>
            <DialogP.Close asChild>
              <Button variant="ghost" size="icon" aria-label="Close">
                <X />
              </Button>
            </DialogP.Close>
          </div>
          <div className="overflow-y-auto px-5 py-4">{children}</div>
          {footer && <div className="flex justify-end gap-2 border-t border-line bg-surface-2 px-5 py-3 rounded-b-xl">{footer}</div>}
        </DialogP.Content>
      </DialogP.Portal>
    </DialogP.Root>
  );
}

/** Imperative confirmation dialog. */
type ConfirmOpts = { title: string; description?: React.ReactNode; confirm?: string; danger?: boolean };
let confirmImpl: ((o: ConfirmOpts) => Promise<boolean>) | null = null;
export const confirmAction = (o: ConfirmOpts) => (confirmImpl ? confirmImpl(o) : Promise.resolve(window.confirm(o.title)));

export function ConfirmHost() {
  const [state, setState] = React.useState<(ConfirmOpts & { resolve: (v: boolean) => void }) | null>(null);
  React.useEffect(() => {
    confirmImpl = (o) => new Promise((resolve) => setState({ ...o, resolve }));
    return () => {
      confirmImpl = null;
    };
  }, []);
  const close = (v: boolean) => {
    state?.resolve(v);
    setState(null);
  };
  return (
    <AlertP.Root open={!!state} onOpenChange={(o) => !o && close(false)}>
      <AlertP.Portal>
        <AlertP.Overlay className="fixed inset-0 z-40 bg-[#0b0e12]/50" />
        <AlertP.Content className="fixed left-1/2 top-[20vh] z-50 w-[calc(100vw-32px)] max-w-md -translate-x-1/2 rounded-xl border border-line bg-surface p-5 shadow-2xl">
          <AlertP.Title className="text-[15px] font-semibold">{state?.title}</AlertP.Title>
          <AlertP.Description className="mt-2 text-[13px] text-ink-2">{state?.description ?? "This cannot be undone."}</AlertP.Description>
          <div className="mt-5 flex justify-end gap-2">
            <AlertP.Cancel asChild>
              <Button>Cancel</Button>
            </AlertP.Cancel>
            <AlertP.Action asChild>
              <Button variant={state?.danger ? "danger" : "primary"} onClick={() => close(true)}>
                {state?.confirm ?? "Confirm"}
              </Button>
            </AlertP.Action>
          </div>
        </AlertP.Content>
      </AlertP.Portal>
    </AlertP.Root>
  );
}

// ---------- Tabs ----------

export const Tabs = TabsP.Root;
export function TabsList({ children, className }: { children: React.ReactNode; className?: string }) {
  // Wraps instead of scrolling: a scrolling tab strip shows scrollbars, and the line under it is drawn
  // as an inset shadow so the active tab's underline can sit on it without overflowing the box.
  return <TabsP.List className={cn("mb-5 flex flex-wrap gap-x-1 shadow-[inset_0_-1px_0_var(--line)]", className)}>{children}</TabsP.List>;
}
export function TabsTrigger({ value, children }: { value: string; children: React.ReactNode }) {
  return (
    <TabsP.Trigger
      value={value}
      className="relative whitespace-nowrap border-b-2 border-transparent px-3 py-2 text-[13px] font-medium text-ink-3 hover:text-ink data-[state=active]:border-blued data-[state=active]:text-ink"
    >
      {children}
    </TabsP.Trigger>
  );
}
export const TabsContent = TabsP.Content;

// ---------- Menu ----------

export function Menu({ trigger, children, align = "end" }: { trigger: React.ReactNode; children: React.ReactNode; align?: "start" | "end" }) {
  return (
    <MenuP.Root>
      <MenuP.Trigger asChild>{trigger}</MenuP.Trigger>
      <MenuP.Portal>
        <MenuP.Content align={align} sideOffset={4} className="z-50 min-w-44 rounded-lg border border-line bg-surface p-1 shadow-xl">
          {children}
        </MenuP.Content>
      </MenuP.Portal>
    </MenuP.Root>
  );
}

export function MenuItem({ children, onSelect, danger, disabled }: { children: React.ReactNode; onSelect: () => void; danger?: boolean; disabled?: boolean }) {
  return (
    <MenuP.Item
      disabled={disabled}
      onSelect={onSelect}
      className={cn(
        "flex cursor-pointer items-center gap-2 rounded-md px-2 py-1.5 text-[13px] outline-none data-[disabled]:opacity-40 data-[highlighted]:bg-sunken [&_svg]:size-4",
        danger ? "text-oxide" : "text-ink",
      )}
    >
      {children}
    </MenuP.Item>
  );
}

export function MenuSeparator() {
  return <MenuP.Separator className="my-1 h-px bg-line" />;
}

// ---------- Tooltip ----------

export function Tip({ content, children }: { content: React.ReactNode; children: React.ReactNode }) {
  return (
    <TipP.Root delayDuration={250}>
      <TipP.Trigger asChild>{children}</TipP.Trigger>
      <TipP.Portal>
        <TipP.Content sideOffset={6} className="z-50 max-w-xs rounded-md bg-ink px-2 py-1 text-xs text-bg shadow-lg">
          {content}
        </TipP.Content>
      </TipP.Portal>
    </TipP.Root>
  );
}
export const TipProvider = TipP.Provider;

/** Editable list of strings (nameservers, CIDRs, domains). Suggestions fill the field as you type. */
export function ListEditor({
  values,
  onChange,
  placeholder,
  mono = true,
  suggestions,
  validate,
  disabled,
}: {
  values: string[];
  onChange: (v: string[]) => void;
  placeholder?: string;
  mono?: boolean;
  suggestions?: SuggestionLike[];
  /** Return an error message to refuse a value. */
  validate?: (v: string) => string | null;
  disabled?: boolean;
}) {
  const [draft, setDraft] = React.useState("");
  const [err, setErr] = React.useState("");
  const add = (raw = draft) => {
    // Pasting "a, b c" adds three entries.
    const parts = raw
      .split(/[\s,]+/)
      .map((x) => x.trim())
      .filter(Boolean);
    if (!parts.length) return;
    const next = [...values];
    for (const v of parts) {
      const bad = validate?.(v);
      if (bad) {
        setErr(bad);
        return;
      }
      if (!next.includes(v)) next.push(v);
    }
    onChange(next);
    setDraft("");
    setErr("");
  };
  const pool = React.useMemo(() => (suggestions ?? []).filter((s) => !values.includes(typeof s === "string" ? s : s.value)), [suggestions, values]);
  return (
    <div className="space-y-2">
      {values.length > 0 && (
        <div className="flex flex-wrap gap-1.5">
          {values.map((v) => (
            <span key={v} className={cn("inline-flex items-center gap-1 rounded-md border border-line bg-surface-2 py-0.5 pl-2 pr-1 text-[12.5px]", mono && "font-mono")}>
              {v}
              {!disabled && (
                <button type="button" aria-label={`Remove ${v}`} className="rounded p-0.5 text-ink-3 hover:bg-sunken hover:text-ink" onClick={() => onChange(values.filter((x) => x !== v))}>
                  <X className="size-3" />
                </button>
              )}
            </span>
          ))}
        </div>
      )}
      {!disabled && (
        <>
          <div className="flex gap-2">
            <SuggestInput
              value={draft}
              placeholder={placeholder}
              mono={mono}
              suggestions={pool}
              onChange={(v) => {
                setDraft(v);
                setErr("");
              }}
              onPick={(v) => add(v)}
              onEnter={() => add()}
              aria-invalid={!!err}
            />
            <Button type="button" onClick={() => add()} disabled={!draft.trim()}>
              Add
            </Button>
          </div>
          {err && <p className="text-xs text-oxide">{err}</p>}
        </>
      )}
    </div>
  );
}
