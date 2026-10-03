using Microsoft.EntityFrameworkCore;

namespace RootAccess.Inventory.Api.Data;

public class InventoryDbContext(DbContextOptions<InventoryDbContext> options) : DbContext(options)
{
}
