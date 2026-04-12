export interface Product {
  id: string;
  name: string;
  price: number;
  category: string;
  color: string;
}

export const PRODUCTS: Product[] = [
  { id: '1', name: 'Running Shoe Pro', price: 129.99, category: 'Shoes', color: '#6366f1' },
  { id: '2', name: 'Leather Messenger Bag', price: 89.00, category: 'Bags', color: '#ec4899' },
  { id: '3', name: 'Smart Watch Ultra', price: 249.00, category: 'Watches', color: '#22c55e' },
  { id: '4', name: 'Polarized Sunglasses', price: 65.00, category: 'Accessories', color: '#f59e0b' },
  { id: '5', name: 'Canvas Backpack', price: 45.00, category: 'Bags', color: '#8b5cf6' },
  // BUG: this product has null price — will crash ProductCard and CartScreen
  // The auto-test demo will find and fix this
  { id: '6', name: 'Wireless Earbuds', price: null as any, category: 'Electronics', color: '#ef4444' },
];
