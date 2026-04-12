import { mutation, query } from "./_generated/server";
import { v } from "convex/values";

export const seedRecipes = mutation({
  args: {},
  handler: async (ctx) => {
    const recipes: any[] = [
      {
        title: "Avocado Toast",
        category: "Quick",
        cookTime: 10,
        rating: 4.5,
        imageUrl: "https://images.unsplash.com/photo-1541519227354-08fa5d50c44d",
        ingredients: [
          { name: "Sourdough Bread", amount: "2 slices", price: 2.5 },
          { name: "Avocado", amount: "1", price: 1.75 },
          { name: "Lemon", amount: "1/2", price: 0.5 },
          { name: "Chili Flakes", amount: "1 tsp", price: null },
          { name: "Sea Salt", amount: "pinch", price: 0.1 },
        ],
        steps: [
          { text: "Toast the bread until golden.", duration: 180 },
          { text: "Mash avocado with lemon juice and salt.", duration: 120 },
          { text: "Spread avocado on toast and top with chili flakes.", duration: 60 },
        ],
      },
      {
        title: "Overnight Oats",
        category: "Healthy",
        cookTime: 5,
        rating: 4.3,
        imageUrl: null,
        ingredients: [
          { name: "Rolled Oats", amount: "1/2 cup", price: 0.8 },
          { name: "Milk", amount: "1 cup", price: 1.2 },
          { name: "Honey", amount: "1 tbsp", price: null },
          { name: "Chia Seeds", amount: "1 tsp", price: 0.4 },
        ],
        steps: [
          { text: "Combine oats, milk, honey, and chia in a jar.", duration: 120 },
          { text: "Stir well and seal the jar.", duration: 30 },
          { text: "Refrigerate overnight." },
          { text: "Stir and serve chilled.", duration: 30 },
        ],
      },
      {
        title: "Greek Salad",
        category: "Healthy",
        cookTime: 10,
        rating: 4.2,
        imageUrl: "https://images.unsplash.com/photo-1540420773420-3366772f4999",
        ingredients: [
          { name: "Cucumber", amount: "1", price: 0.9 },
          { name: "Tomatoes", amount: "2", price: 1.5 },
          { name: "Feta Cheese", amount: "100g", price: 3.2 },
          { name: "Kalamata Olives", amount: "1/4 cup", price: 2.0 },
          { name: "Olive Oil", amount: "2 tbsp", price: null },
        ],
        steps: [
          { text: "Chop cucumber and tomatoes.", duration: 180 },
          { text: "Crumble feta and add olives.", duration: 60 },
          { text: "Drizzle with olive oil and toss.", duration: 30 },
        ],
      },
      {
        title: "Teriyaki Bowl",
        category: "Quick",
        cookTime: 25,
        rating: 4.8,
        imageUrl: "https://images.unsplash.com/photo-1546069901-ba9599a7e63c",
        ingredients: [
          { name: "Chicken Thigh", amount: "300g", price: 5.5 },
          { name: "Rice", amount: "1 cup", price: 1.0 },
          { name: "Teriyaki Sauce", amount: "1/4 cup", price: 2.5 },
          { name: "Broccoli", amount: "1 cup", price: 1.2 },
          { name: "Sesame Seeds", amount: "1 tsp", price: 0.3 },
        ],
        steps: [
          { text: "Cook rice until fluffy.", duration: 900 },
          { text: "Sear chicken in a hot pan.", duration: 420 },
          { text: "Add teriyaki sauce and simmer.", duration: 180 },
          { text: "Steam broccoli.", duration: 240 },
          { text: "Assemble bowl and sprinkle sesame seeds.", duration: 60 },
        ],
      },
      {
        title: "Chicken Stir-Fry",
        category: "Quick",
        cookTime: 20,
        rating: 4.6,
        imageUrl: "https://images.unsplash.com/photo-1603133872878-684f208fb84b",
        ingredients: [
          { name: "Chicken Breast", amount: "250g", price: 4.8 },
          { name: "Bell Peppers", amount: "2", price: 2.4 },
          { name: "Soy Sauce", amount: "3 tbsp", price: 0.6 },
          { name: "Garlic", amount: "2 cloves", price: 0.2 },
        ],
        steps: [
          { text: "Slice chicken and vegetables.", duration: 300 },
          { text: "Stir-fry chicken in hot oil.", duration: 360 },
          { text: "Add peppers and garlic.", duration: 240 },
          { text: "Finish with soy sauce.", duration: 60 },
        ],
      },
      {
        title: "Pasta Carbonara",
        category: "Comfort",
        cookTime: 30,
        rating: 4.9,
        imageUrl: "https://images.unsplash.com/photo-1612874742237-6526221588e3",
        ingredients: [
          { name: "Spaghetti", amount: "400g", price: 2.0 },
          { name: "Guanciale", amount: "150g", price: 6.5 },
          { name: "Egg Yolks", amount: "4", price: 1.6 },
          { name: "Pecorino Romano", amount: "80g", price: 4.2 },
          { name: "Black Pepper", amount: "1 tsp", price: 0.2 },
        ],
        steps: [
          { text: "Boil pasta in salted water.", duration: 600 },
          { text: "Crisp guanciale in a pan.", duration: 300 },
          { text: "Whisk yolks with pecorino and pepper.", duration: 120 },
          { text: "Toss pasta with guanciale off heat.", duration: 60 },
          { text: "Fold in egg mixture until silky.", duration: 60 },
        ],
      },
      {
        title: "Mango Smoothie Bowl",
        category: "Healthy",
        cookTime: 5,
        rating: 4.4,
        imageUrl: "https://images.unsplash.com/photo-1490474504059-bf2db5ab2348",
        ingredients: [
          { name: "Frozen Mango", amount: "1 cup", price: 2.5 },
          { name: "Banana", amount: "1", price: 0.4 },
          { name: "Yogurt", amount: "1/2 cup", price: 1.1 },
          { name: "Granola", amount: "1/4 cup", price: 1.3 },
        ],
        steps: [
          { text: "Blend mango, banana, and yogurt.", duration: 90 },
          { text: "Pour into a bowl.", duration: 15 },
          { text: "Top with granola.", duration: 30 },
        ],
      },
      {
        title: "Chocolate Lava Cake",
        category: "Dessert",
        cookTime: 25,
        rating: 4.7,
        imageUrl: "https://images.unsplash.com/photo-1624353365286-3f8d62daad51",
        ingredients: [
          { name: "Dark Chocolate", amount: "120g", price: 3.5 },
          { name: "Butter", amount: "100g", price: 1.8 },
          { name: "Eggs", amount: "2", price: 0.8 },
          { name: "Sugar", amount: "1/4 cup", price: 0.3 },
          { name: "Flour", amount: "2 tbsp", price: 0.1 },
        ],
        steps: [
          { text: "Melt chocolate and butter together.", duration: 180 },
          { text: "Whisk eggs with sugar until pale.", duration: 180 },
          { text: "Fold in chocolate and flour.", duration: 120 },
          { text: "Pour into ramekins.", duration: 60 },
          { text: "Bake at 220°C until edges set.", duration: 600 },
        ],
      },
    ];

    const ids = [];
    for (const r of recipes) {
      ids.push(await ctx.db.insert("recipes", r));
    }
    return ids;
  },
});

export const list = query({
  args: {},
  handler: async (ctx) => {
    return await ctx.db.query("recipes").collect();
  },
});

export const get = query({
  args: { id: v.id("recipes") },
  handler: async (ctx, { id }) => {
    return await ctx.db.get(id);
  },
});
