import Link from "next/link";
import { Container } from "@/components/Page";

export default function NotFound() {
  return (
    <Container className="py-24">
      <h1 className="t-display">Page not found</h1>
      <p className="t-lead mt-4 text-[var(--text-dim)]">
        Nothing is published at this address.
      </p>
      <p className="mt-6">
        <Link href="/" className="underline text-[var(--accent)]">Back to the home page</Link>
      </p>
    </Container>
  );
}
