package equinix

import (
	"fmt"
	"log"
	"net"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/acctest"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/resource"
	"github.com/hashicorp/terraform-plugin-sdk/v2/terraform"
	"github.com/packethost/packngo"
)

// list of plans and metros used as filter criteria to find available hardware to run tests
var preferable_plans = []string{"x1.small.x86", "t1.small.x86", "c2.medium.x86", "c3.small.x86", "c3.medium.x86", "m3.small.x86"}
var preferable_metros = []string{"ch", "ny", "sv", "ty", "am"}

func init() {
	resource.AddTestSweepers("equinix_metal_device", &resource.Sweeper{
		Name: "equinix_metal_device",
		F:    testSweepDevices,
	})
}

func testSweepDevices(region string) error {
	log.Printf("[DEBUG] Sweeping devices")
	config, err := sharedConfigForRegion(region)
	if err != nil {
		return fmt.Errorf("[INFO][SWEEPER_LOG] Error getting configuration for sweeping devices: %s", err)
	}
	metal := config.NewMetalClient()
	ps, _, err := metal.Projects.List(nil)
	if err != nil {
		return fmt.Errorf("[INFO][SWEEPER_LOG] Error getting project list for sweepeing devices: %s", err)
	}
	pids := []string{}
	for _, p := range ps {
		if isSweepableTestResource(p.Name) {
			pids = append(pids, p.ID)
		}
	}
	dids := []string{}
	for _, pid := range pids {
		ds, _, err := metal.Devices.List(pid, nil)
		if err != nil {
			log.Printf("Error listing devices to sweep: %s", err)
			continue
		}
		for _, d := range ds {
			if isSweepableTestResource(d.Hostname) {
				dids = append(dids, d.ID)
			}
		}
	}

	for _, did := range dids {
		log.Printf("Removing device %s", did)
		_, err := metal.Devices.Delete(did, true)
		if err != nil {
			return fmt.Errorf("Error deleting device %s", err)
		}
	}
	return nil
}

// Regexp vars for use with resource.ExpectError
var matchErrMustBeProvided = regexp.MustCompile(".* must be provided when .*")
var matchErrShouldNotBeAnIPXE = regexp.MustCompile(`.*"user_data" should not be an iPXE.*`)

// This function should be used to find available plans in all test where a metal_device resource is needed.
// To prevent unexpected plan/facilities changes (i.e. run out of a plan in a metro after first apply)
// during tests that have several config updates, resource metal_device should include a lifecycle
// like the one defined below.
//
// lifecycle {
//     ignore_changes = [
//       plan,
//       facilities,
//     ]
//   }
func confAccMetalDevice_base(plans, metros []string) string {
	return fmt.Sprintf(`
data "equinix_metal_plans" "test" {
    filter {
        attribute = "name"
        values    = [%s]
    }
    filter {
        attribute = "available_in_metros"
        values    = [%s]
    }
}

locals {
    //Operations to select a plan randomly and avoid race conditions with metros without capacity.
    //With these operations we use current time seconds as the seed, and avoid using a third party provider in the Equinix provider tests
    plans             = data.equinix_metal_plans.test.plans
    plans_random_num  = formatdate("s", timestamp())
    plans_length      = length(local.plans)
    plans_range_limit = ceil(59 / local.plans_length) == 1 ? local.plans_length : 59
    plan_idxs         = [for idx, value in range(0, local.plans_range_limit, ceil(59 / local.plans_length)) : idx if local.plans_random_num <= value]
    plan_idx          = length(local.plan_idxs) > 0 ? local.plan_idxs[0] : 0
    plan              = local.plans[local.plan_idx].slug

    facilities = tolist(setsubtract(local.plans[local.plan_idx].available_in, ["sjc1", "ld7", "sy4"]))

    //Operations to select a metro randomly and avoid race conditions with metros without capacity.
    //With these operations we use current time seconds as the seed, and avoid using a third party provider in the Equinix provider tests
    metros             = tolist(local.plans[local.plan_idx].available_in_metros)
    metros_random_num  = formatdate("s", timestamp())
    metros_length      = length(local.metros)
    metros_range_limit = ceil(59 / local.metros_length) == 1 ? local.metros_length : 59
    metro_idxs         = [for idx, value in range(0, local.metros_range_limit, ceil(59 / local.metros_length)) : idx if local.metros_random_num <= value]
    metro_idx          = length(local.metro_idxs) > 0 ? local.metro_idxs[0] : 0
    metro              = local.metros[local.metro_idx]
}
`, fmt.Sprintf("\"%s\"", strings.Join(plans[:], `","`)), fmt.Sprintf("\"%s\"", strings.Join(metros[:], `","`)))
}

func testDeviceTerminationTime() string {
	return time.Now().UTC().Add(60 * time.Minute).Format(time.RFC3339)
}

func TestAccMetalDevice_facilityList(t *testing.T) {
	var device packngo.Device
	rs := acctest.RandString(10)
	r := "equinix_metal_device.test"

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: testAccMetalDeviceCheckDestroyed,
		Steps: []resource.TestStep{
			{
				Config: testAccMetalDeviceConfig_facility_list(rs),
				Check: resource.ComposeTestCheckFunc(
					testAccMetalDeviceExists(r, &device),
				),
			},
		},
	})
}

func TestAccMetalDevice_sshConfig(t *testing.T) {
	rs := acctest.RandString(10)
	r := "equinix_metal_device.test"
	userSSHKey, _, err := acctest.RandSSHKeyPair("")
	if err != nil {
		t.Fatalf("Cannot generate test SSH key pair: %s", err)
	}
	projSSHKey, _, err := acctest.RandSSHKeyPair("")
	if err != nil {
		t.Fatalf("Cannot generate test SSH key pair: %s", err)
	}
	resource.ParallelTest(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: testAccMetalDeviceCheckDestroyed,
		Steps: []resource.TestStep{
			{
				Config: testAccMetalDeviceConfig_ssh_key(rs, userSSHKey, projSSHKey),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(
						r, "ssh_key_ids.#", "2"),
				),
			},
		},
	})
}

func TestAccMetalDevice_basic(t *testing.T) {
	var device packngo.Device
	rs := acctest.RandString(10)
	r := "equinix_metal_device.test"

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: testAccMetalDeviceCheckDestroyed,
		Steps: []resource.TestStep{
			{
				Config: testAccMetalDeviceConfig_minimal(rs),
				Check: resource.ComposeTestCheckFunc(
					testAccMetalDeviceExists(r, &device),
					testAccMetalDeviceNetwork(r),
					resource.TestCheckResourceAttrSet(
						r, "hostname"),
					resource.TestCheckResourceAttr(
						r, "billing_cycle", "hourly"),
					resource.TestCheckResourceAttr(
						r, "network_type", "layer3"),
					resource.TestCheckResourceAttr(
						r, "ipxe_script_url", ""),
					resource.TestCheckResourceAttr(
						r, "always_pxe", "false"),
					resource.TestCheckResourceAttrSet(
						r, "root_password"),
					resource.TestCheckResourceAttrPair(
						r, "deployed_facility", r, "facilities.0"),
				),
			},
			{
				Config: testAccMetalDeviceConfig_basic(rs),
				Check: resource.ComposeTestCheckFunc(
					testAccMetalDeviceExists(r, &device),
					testAccMetalDeviceNetwork(r),
					testAccMetalDeviceAttributes(&device),
					testAccMetalDeviceNetworkOrder(r),
					testAccMetalDevicePortsOrder(r),
				),
			},
		},
	})
}

func TestAccMetalDevice_metro(t *testing.T) {
	var device packngo.Device
	rs := acctest.RandString(10)
	r := "equinix_metal_device.test"

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: testAccMetalDeviceCheckDestroyed,
		Steps: []resource.TestStep{
			{
				Config: testAccMetalDeviceConfig_metro(rs),
				Check: resource.ComposeTestCheckFunc(
					testAccMetalDeviceExists(r, &device),
					testAccMetalDeviceNetwork(r),
					testAccMetalDeviceAttributes(&device),
					resource.TestCheckResourceAttr(
						r, "metro", "sv"),
				),
			},
		},
	})
}

func TestAccMetalDevice_update(t *testing.T) {
	var d1, d2, d3, d4, d5 packngo.Device
	rs := acctest.RandString(10)
	rInt := acctest.RandInt()
	r := "equinix_metal_device.test"

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: testAccMetalDeviceCheckDestroyed,
		Steps: []resource.TestStep{
			{
				Config: testAccMetalDeviceConfig_varname(rInt, rs),
				Check: resource.ComposeTestCheckFunc(
					testAccMetalDeviceExists(r, &d1),
					resource.TestCheckResourceAttr(r, "hostname", fmt.Sprintf("tfacc-test-device-%d", rInt)),
				),
			},
			{
				Config: testAccMetalDeviceConfig_varname(rInt+1, rs),
				Check: resource.ComposeTestCheckFunc(
					testAccMetalDeviceExists(r, &d2),
					resource.TestCheckResourceAttr(r, "hostname", fmt.Sprintf("tfacc-test-device-%d", rInt+1)),
					testAccMetalSameDevice(t, &d1, &d2),
				),
			},
			{
				Config: testAccMetalDeviceConfig_varname(rInt+2, rs),
				Check: resource.ComposeTestCheckFunc(
					testAccMetalDeviceExists(r, &d3),
					resource.TestCheckResourceAttr(r, "hostname", fmt.Sprintf("tfacc-test-device-%d", rInt+2)),
					resource.TestCheckResourceAttr(r, "description", fmt.Sprintf("test-desc-%d", rInt+2)),
					resource.TestCheckResourceAttr(r, "tags.0", fmt.Sprintf("%d", rInt+2)),
					testAccMetalSameDevice(t, &d2, &d3),
				),
			},
			{
				Config: testAccMetalDeviceConfig_no_description(rInt+3, rs),
				Check: resource.ComposeTestCheckFunc(
					testAccMetalDeviceExists(r, &d4),
					resource.TestCheckResourceAttr(r, "hostname", fmt.Sprintf("tfacc-test-device-%d", rInt+3)),
					resource.TestCheckResourceAttr(r, "tags.0", fmt.Sprintf("%d", rInt+3)),
					testAccMetalSameDevice(t, &d3, &d4),
				),
			},
			{
				Config: testAccMetalDeviceConfig_reinstall(rInt+4, rs),
				Check: resource.ComposeTestCheckFunc(
					testAccMetalDeviceExists(r, &d5),
					testAccMetalSameDevice(t, &d4, &d5),
				),
			},
		},
	})
}

func TestAccMetalDevice_IPXEScriptUrl(t *testing.T) {
	var device, d2 packngo.Device
	rs := acctest.RandString(10)
	r := "equinix_metal_device.test_ipxe_script_url"

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: testAccMetalDeviceCheckDestroyed,
		Steps: []resource.TestStep{
			{
				Config: testAccMetalDeviceConfig_ipxe_script_url(rs, "https://boot.netboot.xyz", "true"),
				Check: resource.ComposeTestCheckFunc(
					testAccMetalDeviceExists(r, &device),
					testAccMetalDeviceNetwork(r),
					resource.TestCheckResourceAttr(
						r, "ipxe_script_url", "https://boot.netboot.xyz"),
					resource.TestCheckResourceAttr(
						r, "always_pxe", "true"),
				),
			},
			{
				Config: testAccMetalDeviceConfig_ipxe_script_url(rs, "https://new.netboot.xyz", "false"),
				Check: resource.ComposeTestCheckFunc(
					testAccMetalDeviceExists(r, &d2),
					testAccMetalDeviceNetwork(r),
					resource.TestCheckResourceAttr(
						r, "ipxe_script_url", "https://new.netboot.xyz"),
					resource.TestCheckResourceAttr(
						r, "always_pxe", "false"),
					testAccMetalSameDevice(t, &device, &d2),
				),
			},
		},
	})
}

func TestAccMetalDevice_IPXEConflictingFields(t *testing.T) {
	var device packngo.Device
	rs := acctest.RandString(10)
	r := "equinix_metal_device.test_ipxe_conflict"

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: testAccMetalDeviceCheckDestroyed,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(testAccMetalDeviceConfig_ipxe_conflict, confAccMetalDevice_base(preferable_plans, preferable_metros), rs),
				Check: resource.ComposeTestCheckFunc(
					testAccMetalDeviceExists(r, &device),
				),
				ExpectError: matchErrShouldNotBeAnIPXE,
			},
		},
	})
}

func TestAccMetalDevice_IPXEConfigMissing(t *testing.T) {
	var device packngo.Device
	rs := acctest.RandString(10)
	r := "equinix_metal_device.test_ipxe_config_missing"

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: testAccMetalDeviceCheckDestroyed,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(testAccMetalDeviceConfig_ipxe_missing, confAccMetalDevice_base(preferable_plans, preferable_metros), rs),
				Check: resource.ComposeTestCheckFunc(
					testAccMetalDeviceExists(r, &device),
				),
				ExpectError: matchErrMustBeProvided,
			},
		},
	})
}

func testAccMetalDeviceCheckDestroyed(s *terraform.State) error {
	client := testAccProvider.Meta().(*Config).metal

	for _, rs := range s.RootModule().Resources {
		if rs.Type != "equinix_metal_device" {
			continue
		}
		if _, _, err := client.Devices.Get(rs.Primary.ID, nil); err == nil {
			return fmt.Errorf("Metal Device still exists")
		}
	}
	return nil
}

func testAccMetalDeviceAttributes(device *packngo.Device) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		if device.Hostname != "tfacc-test-device" {
			return fmt.Errorf("Bad name: %s", device.Hostname)
		}
		if device.State != "active" {
			return fmt.Errorf("Device should be 'active', not '%s'", device.State)
		}

		return nil
	}
}

func testAccMetalDeviceExists(n string, device *packngo.Device) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[n]
		if !ok {
			return fmt.Errorf("Not found: %s", n)
		}
		if rs.Primary.ID == "" {
			return fmt.Errorf("No Record ID is set")
		}

		client := testAccProvider.Meta().(*Config).metal

		foundDevice, _, err := client.Devices.Get(rs.Primary.ID, nil)
		if err != nil {
			return err
		}
		if foundDevice.ID != rs.Primary.ID {
			return fmt.Errorf("Record not found: %v - %v", rs.Primary.ID, foundDevice)
		}

		*device = *foundDevice

		return nil
	}
}

func testAccMetalSameDevice(t *testing.T, before, after *packngo.Device) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		if before.ID != after.ID {
			t.Fatalf("Expected device to be the same, but it was recreated: %s -> %s", before.ID, after.ID)
		}
		return nil
	}
}

func testAccMetalDevicePortsOrder(n string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[n]
		if !ok {
			return fmt.Errorf("Not found: %s", n)
		}
		if rs.Primary.Attributes["ports.0.name"] != "bond0" {
			return fmt.Errorf("first port should be bond0")
		}
		if rs.Primary.Attributes["ports.1.name"] != "eth0" {
			return fmt.Errorf("second port should be eth0")
		}
		if rs.Primary.Attributes["ports.2.name"] != "eth1" {
			return fmt.Errorf("third port should be eth1")
		}
		return nil
	}
}

func testAccMetalDeviceNetworkOrder(n string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[n]
		if !ok {
			return fmt.Errorf("Not found: %s", n)
		}
		if rs.Primary.Attributes["network.0.family"] != "4" {
			return fmt.Errorf("first netowrk should be public IPv4")
		}
		if rs.Primary.Attributes["network.0.public"] != "true" {
			return fmt.Errorf("first netowrk should be public IPv4")
		}
		if rs.Primary.Attributes["network.1.family"] != "6" {
			return fmt.Errorf("second netowrk should be public IPv6")
		}
		if rs.Primary.Attributes["network.2.family"] != "4" {
			return fmt.Errorf("third netowrk should be private IPv4")
		}
		if rs.Primary.Attributes["network.2.public"] == "true" {
			return fmt.Errorf("third netowrk should be private IPv4")
		}
		return nil
	}
}

func testAccMetalDeviceNetwork(n string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		var ip net.IP
		var k, v string
		rs, ok := s.RootModule().Resources[n]
		if !ok {
			return fmt.Errorf("Not found: %s", n)
		}

		k = "access_public_ipv6"
		v = rs.Primary.Attributes[k]
		ip = net.ParseIP(v)
		if ip == nil {
			return fmt.Errorf("\"%s\" is not a valid IP address: %s",
				k, v)
		}

		k = "access_public_ipv4"
		v = rs.Primary.Attributes[k]
		ip = net.ParseIP(v)
		if ip == nil {
			return fmt.Errorf("\"%s\" is not a valid IP address: %s",
				k, v)
		}

		k = "access_private_ipv4"
		v = rs.Primary.Attributes[k]
		ip = net.ParseIP(v)
		if ip == nil {
			return fmt.Errorf("\"%s\" is not a valid IP address: %s",
				k, v)
		}

		return nil
	}
}

func TestAccMetalDevice_importBasic(t *testing.T) {
	rs := acctest.RandString(10)

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: testAccMetalDeviceCheckDestroyed,
		Steps: []resource.TestStep{
			{
				Config: testAccMetalDeviceConfig_basic(rs),
			},
			{
				ResourceName:      "equinix_metal_device.test",
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
	})
}

func testAccMetalDeviceConfig_no_description(rInt int, projSuffix string) string {
	return fmt.Sprintf(`
%s

resource "equinix_metal_project" "test" {
    name = "tfacc-device-%s"
}

resource "equinix_metal_device" "test" {
  hostname         = "tfacc-test-device-%d"
  plan             = local.plan
  metro            = local.metro
  operating_system = "ubuntu_22_04"
  billing_cycle    = "hourly"
  project_id       = "${equinix_metal_project.test.id}"
  tags             = ["%d"]
  termination_time = "%s"

  lifecycle {
    ignore_changes = [
      plan,
      metro,
    ]
  }
}
`, confAccMetalDevice_base(preferable_plans, preferable_metros), projSuffix, rInt, rInt, testDeviceTerminationTime())
}

func testAccMetalDeviceConfig_reinstall(rInt int, projSuffix string) string {
	return fmt.Sprintf(`
%s

resource "equinix_metal_project" "test" {
    name = "tfacc-device-%s"
}

resource "equinix_metal_device" "test" {
  hostname         = "tfacc-test-device-%d"
  plan             = local.plan
  metro            = local.metro
  operating_system = "ubuntu_22_04"
  billing_cycle    = "hourly"
  project_id       = "${equinix_metal_project.test.id}"
  tags             = ["%d"]
  user_data = "#!/usr/bin/env sh\necho Reinstall\n"
  termination_time = "%s"

  reinstall {
	  enabled = true
	  deprovision_fast = true
  }

  lifecycle {
    ignore_changes = [
      plan,
      metro,
    ]
  }
}
`, confAccMetalDevice_base(preferable_plans, preferable_metros), projSuffix, rInt, rInt, testDeviceTerminationTime())
}

func testAccMetalDeviceConfig_varname(rInt int, projSuffix string) string {
	return fmt.Sprintf(`
%s

resource "equinix_metal_project" "test" {
    name = "tfacc-device-%s"
}

resource "equinix_metal_device" "test" {
  hostname         = "tfacc-test-device-%d"
  description      = "test-desc-%d"
  plan             = local.plan
  metro            = local.metro
  operating_system = "ubuntu_22_04"
  billing_cycle    = "hourly"
  project_id       = "${equinix_metal_project.test.id}"
  tags             = ["%d"]
  termination_time = "%s"

  lifecycle {
    ignore_changes = [
      plan,
      metro,
    ]
  }
}
`, confAccMetalDevice_base(preferable_plans, preferable_metros), projSuffix, rInt, rInt, rInt, testDeviceTerminationTime())
}

func testAccMetalDeviceConfig_varname_pxe(rInt int, projSuffix string) string {
	return fmt.Sprintf(`
%s

resource "equinix_metal_project" "test" {
    name = "tfacc-device-%s"
}

resource "equinix_metal_device" "test" {
  hostname         = "tfacc-test-device-%d"
  description      = "test-desc-%d"
  plan             = local.plan
  metro            = local.metro
  operating_system = "ubuntu_22_04"
  billing_cycle    = "hourly"
  project_id       = "${equinix_metal_project.test.id}"
  tags             = ["%d"]
  always_pxe       = true
  ipxe_script_url  = "http://matchbox.foo.wtf:8080/boot.ipxe"
  termination_time = "%s"

  lifecycle {
    ignore_changes = [
      plan,
      metro,
    ]
  }
}
`, confAccMetalDevice_base(preferable_plans, preferable_metros), projSuffix, rInt, rInt, rInt, testDeviceTerminationTime())
}

func testAccMetalDeviceConfig_metro(projSuffix string) string {
	return fmt.Sprintf(`
%s

resource "equinix_metal_project" "test" {
    name = "tfacc-device-%s"
}

resource "equinix_metal_device" "test" {
  hostname         = "tfacc-test-device"
  plan             = local.plan
  metro            = local.metro
  operating_system = "ubuntu_22_04"
  billing_cycle    = "hourly"
  project_id       = "${equinix_metal_project.test.id}"
  termination_time = "%s"

  lifecycle {
    ignore_changes = [
      plan,
      metro,
    ]
  }
}
`, confAccMetalDevice_base(preferable_plans, preferable_metros), projSuffix, testDeviceTerminationTime())
}

func testAccMetalDeviceConfig_minimal(projSuffix string) string {
	return fmt.Sprintf(`
%s

resource "equinix_metal_project" "test" {
    name = "tfacc-device-%s"
}

resource "equinix_metal_device" "test" {
  plan             = local.plan
  metro            = local.metro
  operating_system = "ubuntu_22_04"
  project_id       = "${equinix_metal_project.test.id}"

  lifecycle {
    ignore_changes = [
      plan,
      metro,
    ]
  }
}`, confAccMetalDevice_base(preferable_plans, preferable_metros), projSuffix)
}

func testAccMetalDeviceConfig_basic(projSuffix string) string {
	return fmt.Sprintf(`
%s

resource "equinix_metal_project" "test" {
    name = "tfacc-device-%s"
}


resource "equinix_metal_device" "test" {
  hostname         = "tfacc-test-device"
  plan             = local.plan
  metro            = local.metro
  operating_system = "ubuntu_22_04"
  billing_cycle    = "hourly"
  project_id       = "${equinix_metal_project.test.id}"
  termination_time = "%s"

  lifecycle {
    ignore_changes = [
      plan,
      metro,
    ]
  }
}`, confAccMetalDevice_base(preferable_plans, preferable_metros), projSuffix, testDeviceTerminationTime())
}

func testAccMetalDeviceConfig_ssh_key(projSuffix, userSSSHKey, projSSHKey string) string {
	return fmt.Sprintf(`
resource "equinix_metal_project" "test" {
    name = "tfacc-device-%s"
}

resource "equinix_metal_ssh_key" "test" {
	name = "tfacc-ssh-key-%s"
	public_key = "%s"
}

resource "equinix_metal_project_ssh_key" "test" {
	project_id = equinix_metal_project.test.id
	name = "tfacc-project-key-%s"
	public_key = "%s"
}

resource "equinix_metal_device" "test" {
	hostname         = "tfacc-test-device"
	plan             = "c3.small.x86"
	metro            = "sv"
	operating_system = "ubuntu_22_04"
	billing_cycle    = "hourly"
	project_id       = equinix_metal_project.test.id
	user_ssh_key_ids = [equinix_metal_ssh_key.test.id]
	project_ssh_key_ids = [equinix_metal_project_ssh_key.test.id]
  }
`, projSSHKey, projSSHKey, userSSSHKey, projSSHKey, projSSHKey)
}

func testAccMetalDeviceConfig_facility_list(projSuffix string) string {
	return fmt.Sprintf(`
%s

resource "equinix_metal_project" "test" {
  name = "tfacc-device-%s"
}

resource "equinix_metal_device" "test"  {

  hostname         = "tfacc-device-test-ipxe-script-url"
  plan             = local.plan
  facilities       = local.facilities
  operating_system = "ubuntu_22_04"
  billing_cycle    = "hourly"
  project_id       = "${equinix_metal_project.test.id}"
  termination_time = "%s"

  lifecycle {
    ignore_changes = [
      plan,
      facilities,
    ]
  }
}`, confAccMetalDevice_base(preferable_plans, preferable_metros), projSuffix, testDeviceTerminationTime())
}

func testAccMetalDeviceConfig_ipxe_script_url(projSuffix, url, pxe string) string {
	return fmt.Sprintf(`
%s

resource "equinix_metal_project" "test" {
  name = "tfacc-device-%s"
}

resource "equinix_metal_device" "test_ipxe_script_url"  {

  hostname         = "tfacc-device-test-ipxe-script-url"
  plan             = local.plan
  metro            = local.metro
  operating_system = "custom_ipxe"
  user_data        = "#!/bin/sh\ntouch /tmp/test"
  billing_cycle    = "hourly"
  project_id       = "${equinix_metal_project.test.id}"
  ipxe_script_url  = "%s"
  always_pxe       = "%s"
  termination_time = "%s"

  lifecycle {
    ignore_changes = [
      plan,
      metro,
    ]
  }
}`, confAccMetalDevice_base(preferable_plans, preferable_metros), projSuffix, url, pxe, testDeviceTerminationTime())
}

var testAccMetalDeviceConfig_ipxe_conflict = `
%s

resource "equinix_metal_project" "test" {
  name = "tfacc-device-%s"
}

resource "equinix_metal_device" "test_ipxe_conflict" {
  hostname         = "tfacc-device-test-ipxe-conflict"
  plan             = local.plan
  metro            = local.metro
  operating_system = "custom_ipxe"
  user_data        = "#!ipxe\nset conflict ipxe_script_url"
  billing_cycle    = "hourly"
  project_id       = "${equinix_metal_project.test.id}"
  ipxe_script_url  = "https://boot.netboot.xyz"
  always_pxe       = true

  lifecycle {
    ignore_changes = [
      plan,
      metro,
    ]
  }
}`

var testAccMetalDeviceConfig_ipxe_missing = `
%s

resource "equinix_metal_project" "test" {
  name = "tfacc-device-%s"
}

resource "equinix_metal_device" "test_ipxe_missing" {
  hostname         = "tfacc-device-test-ipxe-missing"
  plan             = local.plan
  metro            = local.metro
  operating_system = "custom_ipxe"
  billing_cycle    = "hourly"
  project_id       = "${equinix_metal_project.test.id}"
  always_pxe       = true

  lifecycle {
    ignore_changes = [
      plan,
      metro,
    ]
  }
}`
