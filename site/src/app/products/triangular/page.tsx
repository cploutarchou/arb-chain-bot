import { ProductPage } from "@/components/ProductPage";
import { pageMeta } from "@/lib/meta";

export const metadata = pageMeta(
  "Triangular engine",
  "Intra-venue cycles with taker fees on every leg and the same paper executor.",
  "/products/triangular",
);

export default function Page() {
  return <ProductPage name="product-triangular" jurisdiction={false} />;
}
