import { Link } from "react-router";
import { EmptyState } from "@/components/ui";

export function NotFound() {
  return (
    <EmptyState title="This page doesn't exist" action={<Link to="/" className="text-[13px] font-medium text-blued hover:underline">Go to the overview</Link>}>
      The link may be outdated, or you may not have access to it.
    </EmptyState>
  );
}
