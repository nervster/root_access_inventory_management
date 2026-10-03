using Microsoft.AspNetCore.Mvc;
using RootAccess.Inventory.Api.Data;

namespace RootAccess.Inventory.Api.Controllers;

[ApiController]
[Route("api/[controller]")]
public class HealthController(InventoryDbContext db) : ControllerBase
{
    public record HealthResponse(string Status, string Database);

    [HttpGet]
    public async Task<ActionResult<HealthResponse>> Get(CancellationToken cancellationToken)
    {
        var databaseReachable = await db.Database.CanConnectAsync(cancellationToken);
        return new HealthResponse("ok", databaseReachable ? "connected" : "unreachable");
    }
}
