import { ProductPage } from "@/components/ProductPage";
import { pageMeta } from "@/lib/meta";

export const metadata = pageMeta(
  "Automatic paper execution",
  "Every signal executed on paper, scored net of fees, written up in a report.",
  "/products/auto-paper",
);

export default function Page() {
  return <ProductPage name="product-auto-paper" jurisdiction={false} />;
}
